// Command scale is the helper of bench.sh.
//
// It times one command (time), records the machine and the tools that a
// benchmark ran with (meta), and turns the rows that bench.sh collected into the
// JSON file (json). See README.md.
//
//	go run ./tools/scale time -r row.txt -- pravo plan
//	go run ./tools/scale meta -pravo bin/pravo -tofu tofu
//	go run ./tools/scale json -results results.tsv -meta meta.json -o scale-timings.json
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: scale time|meta|json [flags]")
		return 2
	}
	var err error
	switch args[0] {
	case "time":
		var code int
		code, err = timeCmd(args[1:], stderr)
		if err == nil {
			return code
		}
	case "meta":
		err = metaCmd(args[1:], stdout)
	case "json":
		err = jsonCmd(args[1:])
	default:
		fmt.Fprintf(stderr, "scale: unknown command %q\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "scale:", err)
		return 1
	}
	return 0
}

// timeCmd runs a command and records its wall, user and system time in seconds,
// its peak resident set in megabytes and its exit code. The record is the line
// "wall user sys rss exit" in the file that -r names. The command's own output
// goes through. It returns 0 whatever the command exits with: the exit code is in
// the record.
func timeCmd(args []string, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("time", flag.ContinueOnError)
	fs.SetOutput(stderr)
	record := fs.String("r", "", "file that receives: wall user sys rss_mb exit")
	if err := fs.Parse(args); err != nil {
		return 2, nil
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return 2, fmt.Errorf("usage: scale time -r FILE -- command [args...]")
	}
	cmd := exec.Command(rest[0], rest[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	start := time.Now()
	err := cmd.Run()
	wall := time.Since(start)
	code := 0
	var user, sys time.Duration
	rss := 0.0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
		user, sys = cmd.ProcessState.UserTime(), cmd.ProcessState.SystemTime()
		rss = peakRSS(cmd.ProcessState)
	} else if err != nil {
		fmt.Fprintln(stderr, "scale:", err)
		code = 127
	}
	line := fmt.Sprintf("%.3f %.3f %.3f %.1f %d\n", wall.Seconds(), user.Seconds(), sys.Seconds(), rss, code)
	if *record == "" {
		fmt.Fprint(stderr, line)
		return 0, nil
	}
	return 0, os.WriteFile(*record, []byte(line), 0o644)
}

// Machine describes the computer the benchmark ran on.
type Machine struct {
	Model     string `json:"model"`
	CPUs      int    `json:"cpus"`
	MemoryGB  int    `json:"memory_gb"`
	OS        string `json:"os"`
	LoadStart string `json:"load_start,omitempty"`
	LoadEnd   string `json:"load_end,omitempty"`
}

// Build identifies what was measured.
type Build struct {
	// PravoSHA256 is the digest of the pravo binary that ran.
	PravoSHA256 string `json:"pravo_sha256,omitempty"`
	// Revision is the commit of the module with "-dirty" added when the tree held
	// changes that the commit lacks, as git describe prints it. The digest says
	// which binary ran when the tree was dirty.
	Revision string `json:"revision,omitempty"`
}

// Meta is the header of the benchmark file.
type Meta struct {
	Date      string            `json:"date"`
	Machine   Machine           `json:"machine"`
	Build     Build             `json:"build"`
	Tools     map[string]string `json:"tools"`
	Providers map[string]string `json:"providers"`
	Reps      int               `json:"reps"`
	Sizes     []int             `json:"sizes"`
	Notes     []string          `json:"notes,omitempty"`
}

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// metaCmd prints the header of a benchmark file.
func metaCmd(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("meta", flag.ContinueOnError)
	pravo := fs.String("pravo", "pravo", "the pravo binary")
	root := fs.String("root", ".", "the directory of the module, for the revision")
	tofu := fs.String("tofu", "tofu", "the tofu binary")
	reps := fs.Int("reps", 3, "repetitions of each scenario")
	sizes := fs.String("sizes", "", "the sizes that ran, separated by spaces")
	loadStart := fs.String("load-start", "", "the load average when the run began")
	loadEnd := fs.String("load-end", "", "the load average when the run ended")
	var providers, notes listFlag
	fs.Var(&providers, "provider", "TOOL=NAME@VERSION of the provider the tool used; repeatable")
	fs.Var(&notes, "note", "a remark for the header; repeatable")
	if err := fs.Parse(args); err != nil {
		return err
	}
	now := time.Now().UTC()
	m := Meta{
		Date:      now.Format(time.RFC3339),
		Machine:   machine(),
		Tools:     map[string]string{},
		Providers: map[string]string{},
		Reps:      *reps,
		Notes:     notes,
	}
	m.Machine.LoadStart, m.Machine.LoadEnd = strings.TrimSpace(*loadStart), strings.TrimSpace(*loadEnd)
	m.Build = build(*pravo, *root)
	for _, f := range strings.Fields(*sizes) {
		n, err := strconv.Atoi(f)
		if err != nil {
			return fmt.Errorf("size %q: %w", f, err)
		}
		m.Sizes = append(m.Sizes, n)
	}
	if v := firstLine(*pravo, "version"); v != "" {
		m.Tools["pravo"] = v
	}
	if out, err := exec.Command(*pravo, "version").Output(); err == nil {
		for _, l := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(l, "cue ") {
				m.Tools["cue"] = strings.TrimSpace(l)
			}
		}
	}
	if v := firstLine(*tofu, "version"); v != "" {
		m.Tools["tofu"] = v
	}
	m.Tools["go"] = firstLine("go", "version")
	for _, p := range providers {
		key, version, ok := strings.Cut(p, "=")
		if !ok {
			return fmt.Errorf("provider %q: want TOOL=NAME@VERSION", p)
		}
		m.Providers[key] = version
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(m)
}

// build digests the pravo binary and reads the revision of the module.
func build(pravo, root string) Build {
	var b Build
	if path, err := exec.LookPath(pravo); err == nil {
		if f, err := os.Open(path); err == nil {
			h := sha256.New()
			if _, err := io.Copy(h, f); err == nil {
				b.PravoSHA256 = hex.EncodeToString(h.Sum(nil))
			}
			f.Close()
		}
	}
	b.Revision = firstLine("git", "-C", root, "describe", "--always", "--dirty", "--abbrev=12")
	return b
}

func firstLine(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	l, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(l)
}

func machine() Machine {
	m := Machine{CPUs: runtime.NumCPU()}
	switch runtime.GOOS {
	case "darwin":
		m.Model = firstLine("sysctl", "-n", "machdep.cpu.brand_string")
		if b, err := strconv.ParseInt(firstLine("sysctl", "-n", "hw.memsize"), 10, 64); err == nil {
			m.MemoryGB = int(math.Round(float64(b) / (1 << 30)))
		}
		m.OS = "macOS " + firstLine("sw_vers", "-productVersion") + " (" + firstLine("uname", "-sr") + " " + firstLine("uname", "-m") + ")"
	default:
		if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if k, v, ok := strings.Cut(l, ":"); ok && strings.TrimSpace(k) == "model name" {
					m.Model = strings.TrimSpace(v)
					break
				}
			}
		}
		if b, err := os.ReadFile("/proc/meminfo"); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if f := strings.Fields(l); len(f) >= 2 && f[0] == "MemTotal:" {
					if kb, err := strconv.ParseInt(f[1], 10, 64); err == nil {
						m.MemoryGB = int(math.Round(float64(kb) / (1 << 20)))
					}
				}
			}
		}
		m.OS = firstLine("uname", "-sr") + " " + firstLine("uname", "-m")
	}
	return m
}

// Row is the median of the repetitions of one scenario of one tool at one size.
type Row struct {
	Tool     string `json:"tool"`
	N        int    `json:"n"`
	Scenario string `json:"scenario"`
	// Reps is how many repetitions the medians are of.
	Reps int `json:"reps"`
	// WallS, UserS and SysS are medians in seconds; RSSMB is the median of the
	// peak resident set in megabytes.
	WallS float64 `json:"wall_s"`
	UserS float64 `json:"user_s"`
	SysS  float64 `json:"sys_s"`
	RSSMB float64 `json:"rss_mb"`
	// Reads counts the lines "Refreshing state", one for each record read from
	// its provider. Locks counts the locks an apply took, and PlanFileBytes is the
	// size of a saved plan; both are null where they do not apply.
	Reads         int       `json:"reads"`
	Locks         *int      `json:"locks"`
	PlanFileBytes *int      `json:"plan_file_bytes"`
	Exit          int       `json:"exit"`
	WallAll       []float64 `json:"wall_s_all"`
	Summary       string    `json:"summary,omitempty"`
}

// Doc is the benchmark file.
type Doc struct {
	Schema int `json:"schema"`
	Meta
	Rows []Row `json:"rows"`
	// By addresses the same rows by size and scenario, for a reader that wants one
	// value: by.n2000.plan_change_default.pravo.wall_s. Speedup is the wall time of
	// tofu divided by the wall time of pravo.
	By map[string]map[string]map[string]any `json:"by"`
}

// jsonCmd reads the rows of a run and writes the file.
func jsonCmd(args []string) error {
	fs := flag.NewFlagSet("json", flag.ContinueOnError)
	results := fs.String("results", "", "the rows of the run (results.tsv)")
	meta := fs.String("meta", "", "the header (output of scale meta)")
	out := fs.String("o", "", "the file to write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *results == "" || *meta == "" || *out == "" {
		return fmt.Errorf("usage: scale json -results FILE -meta FILE -o FILE")
	}
	raw, err := os.ReadFile(*results)
	if err != nil {
		return err
	}
	hb, err := os.ReadFile(*meta)
	if err != nil {
		return err
	}
	var m Meta
	if err := json.Unmarshal(hb, &m); err != nil {
		return fmt.Errorf("%s: %w", *meta, err)
	}
	doc, err := assemble(string(raw), m)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(*out, append(b, '\n'), 0o644)
}

// sample is one line of results.tsv: tool, n, scenario, rep, wall, user, sys,
// rss, exit, reads, locks, plan file bytes and a summary. A repetition 0 is a
// run made only to count the locks of an apply. A dash is a value that does not
// apply.
type sample struct {
	tool, scenario, summary string
	n, rep, exit, reads     int
	wall, user, sys, rss    float64
	locks, planFile         *int
}

func parseSamples(tsv string) ([]sample, error) {
	var out []sample
	for i, line := range strings.Split(tsv, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 12 {
			return nil, fmt.Errorf("line %d: %d fields, want 12 or 13", i+1, len(f))
		}
		var s sample
		var err error
		s.tool, s.scenario = f[0], f[2]
		if len(f) > 12 {
			s.summary = f[12]
		}
		ints := []struct {
			to  *int
			src string
		}{{&s.n, f[1]}, {&s.rep, f[3]}, {&s.exit, f[8]}, {&s.reads, f[9]}}
		for _, x := range ints {
			if *x.to, err = strconv.Atoi(x.src); err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
		}
		floats := []struct {
			to  *float64
			src string
		}{{&s.wall, f[4]}, {&s.user, f[5]}, {&s.sys, f[6]}, {&s.rss, f[7]}}
		for _, x := range floats {
			if *x.to, err = strconv.ParseFloat(x.src, 64); err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
		}
		for j, to := range []**int{&s.locks, &s.planFile} {
			if f[10+j] == "-" {
				continue
			}
			v, err := strconv.Atoi(f[10+j])
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			*to = &v
		}
		out = append(out, s)
	}
	return out, nil
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

func round(v float64, places int) float64 {
	p := math.Pow10(places)
	return math.Round(v*p) / p
}

func medianInt(v []int) int {
	f := make([]float64, len(v))
	for i, x := range v {
		f[i] = float64(x)
	}
	return int(math.Round(median(f)))
}

// assemble groups the samples of a run into rows of medians.
func assemble(tsv string, m Meta) (*Doc, error) {
	samples, err := parseSamples(tsv)
	if err != nil {
		return nil, err
	}
	type key struct {
		tool, scenario string
		n              int
	}
	groups := map[key][]sample{}
	var order []key
	for _, s := range samples {
		k := key{s.tool, s.scenario, s.n}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], s)
	}
	doc := &Doc{Schema: 1, Meta: m, By: map[string]map[string]map[string]any{}}
	for _, k := range order {
		var wall, user, sys, rss []float64
		var reads, locks, plans []int
		var summary string
		exit := 0
		for _, s := range groups[k] {
			if s.locks != nil {
				locks = append(locks, *s.locks)
			}
			if s.rep == 0 {
				continue // counted the locks only
			}
			wall, user, sys, rss = append(wall, s.wall), append(user, s.user), append(sys, s.sys), append(rss, s.rss)
			reads = append(reads, s.reads)
			if s.planFile != nil {
				plans = append(plans, *s.planFile)
			}
			if s.exit > exit {
				exit = s.exit
			}
			summary = s.summary
		}
		if len(wall) == 0 {
			continue
		}
		r := Row{Tool: k.tool, N: k.n, Scenario: k.scenario, Reps: len(wall),
			WallS: round(median(wall), 3), UserS: round(median(user), 3), SysS: round(median(sys), 3), RSSMB: round(median(rss), 1),
			Reads: medianInt(reads), Exit: exit, Summary: summary}
		for _, w := range wall {
			r.WallAll = append(r.WallAll, round(w, 3))
		}
		if len(locks) > 0 {
			l := medianInt(locks)
			r.Locks = &l
		}
		if len(plans) > 0 {
			p := medianInt(plans)
			r.PlanFileBytes = &p
		}
		doc.Rows = append(doc.Rows, r)
	}
	for _, r := range doc.Rows {
		size := "n" + strconv.Itoa(r.N)
		if doc.By[size] == nil {
			doc.By[size] = map[string]map[string]any{}
		}
		if doc.By[size][r.Scenario] == nil {
			doc.By[size][r.Scenario] = map[string]any{}
		}
		doc.By[size][r.Scenario][r.Tool] = r
	}
	for _, scenarios := range doc.By {
		for _, tools := range scenarios {
			p, okP := tools["pravo"].(Row)
			t, okT := tools["tofu"].(Row)
			if okP && okT && p.WallS > 0 {
				tools["speedup"] = round(t.WallS/p.WallS, 1)
			}
		}
	}
	return doc, nil
}

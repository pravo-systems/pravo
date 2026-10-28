// Command serveshots makes the pictures of the documentation of pravo serve.
//
// For each scenario in tools/serveshots/scenarios it copies the estate to a
// temporary workspace, runs pravo init and pravo apply, makes the changes that
// the shots need (a file edited and another deleted on disk and a pravo drift,
// a resource added to the specification and one removed, a saved plan; for
// the team, a state in a bucket that the program serves, sealed to three
// people with roles, policies, plans stored and approved, a discovery and
// recorded runs), starts pravo serve on a free loopback port and opens the
// address of each shot in headless Chrome at a device scale factor of 2. The
// PNGs are named by the shot, reduced to a palette of 256 colours and written
// to the output directory, docs/img/serve in the module. The program stops on
// a browser that fails, on an image that is blank or has the wrong size, and
// on an error that the page logs to its console.
//
//	go run ./tools/serveshots -bin bin/pravo -out docs/img/serve
//	go run ./tools/serveshots -bin bin/pravo -out docs/img/serve -only overview-dark
//	go run ./tools/serveshots -out docs/img/serve -placeholders
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage: serveshots -bin PATH -out DIR [-chrome PATH] [-fakeprovider PATH] [-only NAMES] [-work DIR] [-keep] [-wait D] [-colours N] [-v]
       serveshots -out DIR -placeholders [-only NAMES]
       serveshots -list

serveshots makes the screenshots of the documentation of pravo serve: one PNG
for each shot, named by the shot, written to DIR. -only takes shot names or
scenario names, comma separated.

`

// run is main with its streams and arguments passed in. It returns the exit
// status: 0 when every shot was made, 1 when a shot failed, 2 when the program
// cannot do its work.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("serveshots", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, usage)
		flags.PrintDefaults()
	}
	bin := flags.String("bin", "", "pravo binary to run (required unless -placeholders or -list)")
	out := flags.String("out", "", "directory for the PNGs")
	chromePath := flags.String("chrome", "", "Chrome or Chromium binary (default: $CHROME, then the usual places)")
	fakeProvider := flags.String("fakeprovider", "", "the fake provider binary for the team situation (default: built from the module with go build)")
	colours := flags.Int("colours", defaultColours, "the colours of the palette each PNG is reduced to; 0 keeps the browser's PNG")
	only := flags.String("only", "", "make only these shots or the shots of these scenarios, comma separated")
	work := flags.String("work", "", "directory for the workspaces (default: a temporary directory, removed afterwards)")
	keep := flags.Bool("keep", false, "keep the workspaces and print where they are")
	wait := flags.Duration("wait", 20*time.Second, "the longest that a page gets to settle before the screenshot")
	placeholders := flags.Bool("placeholders", false, "write flat placeholder images without running anything")
	list := flags.Bool("list", false, "print the shots and exit")
	verbose := flags.Bool("v", false, "print every command that runs")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "serveshots: unexpected argument %q\n", flags.Arg(0))
		return 2
	}
	if err := validateShots(allShots()); err != nil {
		fmt.Fprintf(stderr, "serveshots: %v\n", err)
		return 2
	}
	shots, err := selectShots(allShots(), *only)
	if err != nil {
		fmt.Fprintf(stderr, "serveshots: %v\n", err)
		return 2
	}

	if *list {
		for _, s := range shots {
			fmt.Fprintf(stdout, "%-24s %-9s %-8s %4dx%-4d %s\n", s.Name, s.Scenario, s.Situation, s.Width, s.Height, s.URLFragment())
		}
		return 0
	}
	if *out == "" {
		fmt.Fprintln(stderr, "serveshots: -out is required")
		return 2
	}
	if *placeholders {
		for _, s := range shots {
			if err := writePlaceholder(filepath.Join(*out, s.File()), s); err != nil {
				fmt.Fprintf(stderr, "serveshots: %v\n", err)
				return 2
			}
			fmt.Fprintf(stdout, "placeholder %s (%dx%d)\n", s.File(), s.Width, s.Height)
		}
		return 0
	}
	if *bin == "" {
		fmt.Fprintln(stderr, "serveshots: -bin is required")
		return 2
	}
	binAbs, err := filepath.Abs(*bin)
	if err != nil {
		fmt.Fprintf(stderr, "serveshots: %v\n", err)
		return 2
	}
	if *colours < 0 || *colours > 256 {
		fmt.Fprintf(stderr, "serveshots: -colours %d: a palette holds 1 to 256 colours\n", *colours)
		return 2
	}
	if info, err := os.Stat(binAbs); err != nil || !info.Mode().IsRegular() {
		fmt.Fprintf(stderr, "serveshots: -bin %s: no such file\n", *bin)
		return 2
	}
	chrome, err := findChrome(*chromePath)
	if err != nil {
		fmt.Fprintf(stderr, "serveshots: %v\n", err)
		return 2
	}
	outAbs, err := filepath.Abs(*out)
	if err != nil {
		fmt.Fprintf(stderr, "serveshots: %v\n", err)
		return 2
	}
	if err := os.MkdirAll(outAbs, 0o755); err != nil {
		fmt.Fprintf(stderr, "serveshots: %v\n", err)
		return 2
	}
	workDir := *work
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "serveshots-")
		if err != nil {
			fmt.Fprintf(stderr, "serveshots: %v\n", err)
			return 2
		}
		if !*keep {
			defer os.RemoveAll(workDir)
		}
	} else if err := os.MkdirAll(workDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "serveshots: %v\n", err)
		return 2
	}
	if *keep {
		defer fmt.Fprintf(stdout, "workspaces kept in %s\n", workDir)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var log io.Writer
	if *verbose {
		log = stderr
	}
	m := &maker{
		ctx:      ctx,
		runner:   &pravoRunner{Bin: binAbs, Log: log},
		bin:      binAbs,
		chrome:   chrome,
		provider: *fakeProvider,
		out:      outAbs,
		work:     workDir,
		opts:     shotOptions{Min: time.Second, Max: *wait},
		colours:  *colours,
		log:      log,
		spaces:   map[string]*workspace{},
		views:    map[string]*viewer{},
	}
	defer m.close()

	made, failed := 0, 0
	for _, s := range shots {
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "serveshots: interrupted")
			return 2
		}
		info, notes, err := m.make(s)
		if err != nil {
			failed++
			fmt.Fprintf(stderr, "FAIL %s: %v\n", s.File(), err)
			continue
		}
		made++
		fmt.Fprintf(stdout, "made %-26s %dx%d, %d KB\n", s.File(), info.Width, info.Height, (info.Bytes+1023)/1024)
		for _, n := range notes {
			fmt.Fprintf(stdout, "     console: %s\n", n)
		}
	}
	fmt.Fprintf(stdout, "%d of %d shots made", made, len(shots))
	if failed > 0 {
		fmt.Fprintf(stdout, ", %d failed\n", failed)
		return 1
	}
	fmt.Fprintln(stdout)
	return 0
}

// maker prepares the workspaces and the servers that the shots need, once for
// each, and takes the pictures.
type maker struct {
	ctx    context.Context
	runner *pravoRunner
	bin    string
	chrome string
	// provider is the fake provider binary, built when it is empty and a
	// team situation needs it.
	provider string
	out      string
	work     string
	opts     shotOptions
	colours  int
	log      io.Writer
	// spaces is keyed by scenario and kind of workspace ("platform/clean"),
	// views by scenario and situation ("platform/plan").
	spaces map[string]*workspace
	views  map[string]*viewer
	// store serves the bucket of the team situations, started with the first.
	store *store
}

func (m *maker) logf(format string, args ...any) {
	if m.log != nil {
		fmt.Fprintf(m.log, format+"\n", args...)
	}
}

// workspace returns the workspace for the scenario, made once.
func (m *maker) workspace(sc scenario, situation string) (*workspace, error) {
	kind := workspaceKind(situation)
	key := sc.Name + "/" + kind
	if w, ok := m.spaces[key]; ok {
		return w, nil
	}
	m.logf("preparing %s", key)
	dir := filepath.Join(m.work, kind, sc.Name)
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	var (
		w   *workspace
		err error
	)
	switch kind {
	case situationClean:
		w, err = prepareClean(m.ctx, m.runner, sc, dir)
	case situationStored:
		w, err = prepareStored(m.ctx, m.runner, sc, dir)
	case situationTeam:
		var deps teamDeps
		deps, err = m.teamDeps()
		if err == nil {
			w, err = prepareTeam(m.ctx, m.runner, sc, dir, deps)
		}
	default:
		w, err = prepareChanged(m.ctx, m.runner, sc, dir)
	}
	if err != nil {
		return nil, fmt.Errorf("scenario %s, %s: %w", sc.Name, kind, err)
	}
	m.spaces[key] = w
	return w, nil
}

// viewer returns the server for the scenario and situation, started once.
func (m *maker) viewer(sc scenario, situation string) (*viewer, error) {
	key := sc.Name + "/" + situation
	if v, ok := m.views[key]; ok {
		return v, nil
	}
	w, err := m.workspace(sc, situation)
	if err != nil {
		return nil, err
	}
	plan := ""
	if situation == situationPlan {
		plan = w.Plan
	}
	m.logf("starting pravo serve for %s", key)
	v, err := startViewer(m.ctx, m.bin, w.Dir, plan, w.Env, m.log)
	if err != nil {
		return nil, fmt.Errorf("scenario %s, %s: %w", sc.Name, situation, err)
	}
	m.views[key] = v
	return v, nil
}

// make takes the picture of one shot and checks it. The notes are what the
// page logged to its console that is not an error.
func (m *maker) make(s Shot) (info imageInfo, notes []string, err error) {
	sc, ok := findScenario(s.Scenario)
	if !ok {
		return info, nil, fmt.Errorf("unknown scenario %q", s.Scenario)
	}
	v, err := m.viewer(sc, s.Situation)
	if err != nil {
		return info, nil, err
	}
	target := filepath.Join(m.out, s.File())
	// The browser writes to a name of its own first, so that a failed run leaves
	// the picture that was there.
	tmp := filepath.Join(m.work, "shots", s.File())
	if err := os.MkdirAll(filepath.Dir(tmp), 0o755); err != nil {
		return info, nil, err
	}
	profile := filepath.Join(m.work, "chrome-profile")
	fragment, err := resolveLabels(s.URLFragment(), m.spaces[sc.Name+"/"+workspaceKind(s.Situation)])
	if err != nil {
		return info, nil, fmt.Errorf("shot %s: %w", s.Name, err)
	}
	m.logf("shot %s: %s", s.Name, fragment)
	opts := m.opts
	opts.Replace = m.replacements(sc, s.Situation, v)
	notes, err = screenshotRetry(m.ctx, m.chrome, profile, tmp, v.open(fragment), s.Width, s.Height, opts)
	if err != nil {
		return info, notes, err
	}
	info, err = checkImage(tmp, s.Width*scale, s.Height*scale)
	if err != nil {
		return info, notes, err
	}
	if err := checkTheme(info, s.Fragment.Theme); err != nil {
		return info, notes, err
	}
	if err := shrinkPNG(tmp, m.colours); err != nil {
		return info, notes, fmt.Errorf("shot %s: %w", s.Name, err)
	}
	if info.Bytes, err = fileSize(tmp); err != nil {
		return info, notes, err
	}
	return info, notes, copyFile(tmp, target)
}

// teamDeps starts the store and builds the fake provider, once.
func (m *maker) teamDeps() (teamDeps, error) {
	if m.store == nil {
		s, err := startStore()
		if err != nil {
			return teamDeps{}, err
		}
		m.store = s
		m.logf("serving the bucket of the team on %s", s.Endpoint)
	}
	if m.provider == "" {
		m.logf("building the fake provider")
		p, err := buildFakeProvider(m.ctx, filepath.Join(m.work, "bin"))
		if err != nil {
			return teamDeps{}, err
		}
		m.provider = p
	}
	p, err := filepath.Abs(m.provider)
	if err != nil {
		return teamDeps{}, err
	}
	if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() {
		return teamDeps{}, fmt.Errorf("-fakeprovider %s: no such file", m.provider)
	}
	return teamDeps{Store: m.store, FakeProvider: p}, nil
}

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// replacements are the pairs of text that a picture shows in place of what
// belongs to the machine: the directory of the workspace, as it was given and
// as the system resolves it, becomes ~/estate/NAME, and the address of the
// server becomes 127.0.0.1:8700.
func (m *maker) replacements(sc scenario, situation string, v *viewer) [][2]string {
	w, ok := m.spaces[sc.Name+"/"+workspaceKind(situation)]
	if !ok {
		return nil
	}
	to := "~/estate/" + sc.Name
	dirs := []string{w.Dir}
	if real, err := filepath.EvalSymlinks(w.Dir); err == nil && real != w.Dir {
		dirs = append(dirs, real)
	}
	var pairs [][2]string
	for _, d := range dirs {
		pairs = append(pairs, [2]string{d, to})
		if slash := filepath.ToSlash(d); slash != d {
			pairs = append(pairs, [2]string{slash, to})
		}
	}
	// The longest first: /var/folders/x/y is the end of /private/var/folders/x/y,
	// which a system that links /var to /private/var shows, and replacing the
	// short one first would leave "/private" in front of the replacement.
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i][0]) > len(pairs[j][0]) })
	if _, hostPort, ok := strings.Cut(v.Base, "://"); ok {
		pairs = append(pairs, [2]string{hostPort, "127.0.0.1:8700"})
	}
	return pairs
}

// workspaceKind is the kind of workspace that a situation is made in.
func workspaceKind(situation string) string {
	switch situation {
	case situationClean, situationStored, situationTeam:
		return situation
	}
	return situationChanged
}

// close stops the servers.
func (m *maker) close() {
	for _, v := range m.views {
		v.stop()
	}
	if m.store != nil {
		m.store.stop()
	}
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o644)
}

// validateShots checks the list of shots: unique names, known scenarios and
// situations, valid fragments with motion off, and a theme that agrees with
// the name.
func validateShots(shots []Shot) error {
	seen := map[string]bool{}
	for _, s := range shots {
		if seen[s.Name] {
			return fmt.Errorf("shot %s is listed twice", s.Name)
		}
		seen[s.Name] = true
		sc, ok := findScenario(s.Scenario)
		if !ok {
			return fmt.Errorf("shot %s: unknown scenario %q", s.Name, s.Scenario)
		}
		switch s.Situation {
		case situationClean:
		case situationChanged, situationPlan:
			if !sc.hasChanges() {
				return fmt.Errorf("shot %s: scenario %s has no changed situation", s.Name, s.Scenario)
			}
		case situationStored:
			if !sc.hasStored() {
				return fmt.Errorf("shot %s: scenario %s has no stored plans", s.Name, s.Scenario)
			}
		case situationTeam:
			if !sc.hasTeam() {
				return fmt.Errorf("shot %s: scenario %s has no team situation", s.Name, s.Scenario)
			}
		default:
			return fmt.Errorf("shot %s: unknown situation %q", s.Name, s.Situation)
		}
		if err := s.Fragment.validate(); err != nil {
			return fmt.Errorf("shot %s: %w", s.Name, err)
		}
		if label, ok := strings.CutPrefix(s.Fragment.Plan, "@"); ok {
			if (s.Situation != situationStored && s.Situation != situationTeam) || !slices.Contains(sc.planLabels(), label) {
				return fmt.Errorf("shot %s: plan=@%s needs the stored or the team situation and a stored plan of that name", s.Name, label)
			}
		}
		if label, ok := strings.CutPrefix(s.Fragment.Trace, "@"); ok {
			if s.Situation != situationTeam || !slices.Contains(sc.traceLabels(), label) {
				return fmt.Errorf("shot %s: trace=@%s needs the team situation and a recorded run of that name", s.Name, label)
			}
		}
		if s.Fragment.Motion != "off" {
			return fmt.Errorf("shot %s: the fragment lacks motion=off", s.Name)
		}
		if s.Fragment.Theme == "" {
			return fmt.Errorf("shot %s: the fragment has no theme", s.Name)
		}
		for _, suffix := range []string{"dark", "light"} {
			if strings.HasSuffix(s.Name, "-"+suffix) && s.Fragment.Theme != suffix {
				return fmt.Errorf("shot %s: the name says %s and the fragment says %s", s.Name, suffix, s.Fragment.Theme)
			}
		}
		if s.Width <= 0 || s.Height <= 0 {
			return fmt.Errorf("shot %s: no size", s.Name)
		}
	}
	return nil
}

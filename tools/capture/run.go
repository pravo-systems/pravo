package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// config is what a run needs besides the pages.
type config struct {
	Update  bool
	Verbose bool
	Keep    bool
	Timeout time.Duration
	Log     io.Writer // progress lines for -v
}

// runner executes the commands of pages. It is safe for concurrent use: all
// per-page state lives in pageRun.
type runner struct {
	cfg       config
	env       []string // environment of every command
	home      string
	pravoHome string   // $PRAVO_HOME of the caller, "" when it is not set
	tmp       []string // temporary directories to hide from output

	// initMu serialises the blocks that run "pravo init". Installing a
	// provider writes into PRAVO_HOME, which every page shares, and two pages
	// that unpack the same provider at once would corrupt it. A lock file in
	// the temporary directory does the same between capture processes.
	initMu   sync.Mutex
	initLock string

	services map[string]string // service name -> executable, for the pages that list services
	gate     portGate          // keeps pages that name one port apart
}

// reInit matches a command line that runs pravo init.
var reInit = regexp.MustCompile(`(?m)\bpravo\b.*\binit\b`)

// syncWriter serialises writes from pages that run in parallel.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// newRunner prepares the environment of the commands: the caller's, with
// the directory that holds pravo first on PATH. Variables that change what
// pravo prints for reasons that are not part of the example are removed.
func newRunner(cfg config, binDir string) *runner {
	drop := map[string]bool{"PWD": true, "NO_COLOR": true, "PRAVO_LOG": true, "PRAVO_ACTOR": true, "PRAVO_REASON": true, "TRACEPARENT": true, "TRACESTATE": true}
	var env []string
	havePath := false
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case drop[k]:
		case k == "PATH":
			env = append(env, "PATH="+binDir+string(os.PathListSeparator)+v)
			havePath = true
		default:
			env = append(env, kv)
		}
	}
	if !havePath {
		env = append(env, "PATH="+binDir)
	}
	env = append(env, "NO_COLOR=1")
	home, _ := os.UserHomeDir()
	// A PRAVO_HOME of the caller is where pravo keeps its providers, wherever it
	// is: the output shows it as ~/.pravo, the default.
	pravoHome := os.Getenv("PRAVO_HOME")
	lockHome := pravoHome
	if lockHome == "" {
		lockHome = filepath.Join(home, ".pravo")
	}
	h := fnv.New32a()
	h.Write([]byte(lockHome))
	return &runner{cfg: cfg, env: env, home: home, pravoHome: pravoHome, tmp: []string{os.TempDir()},
		initLock: filepath.Join(lockDir(), fmt.Sprintf("capture-init-%08x.lock", h.Sum32()))}
}

// maxOutput caps what one command may print into the comparison.
const maxOutput = 8 << 20

type limitedBuffer struct {
	buf       bytes.Buffer
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := maxOutput - b.buf.Len()
	if room <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		b.buf.Write(p[:room])
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

// execResult is the outcome of one shell invocation.
type execResult struct {
	Output   []byte
	Exit     int
	TimedOut bool
	Err      error // the shell could not be started, or the run was interrupted
	Took     time.Duration
}

// exec runs script with "sh -c" in dir, or with "sh -e -c" when strict is
// set, which stops the script at the first command that fails. Standard
// output and standard error arrive in one stream, in the order they were
// written. extra is added to the environment of the command.
func (r *runner) exec(ctx context.Context, dir, script string, strict bool, extra []string) execResult {
	cctx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()
	args := []string{"-c", script}
	if strict {
		args = []string{"-e", "-c", script}
	}
	cmd := exec.CommandContext(cctx, "sh", args...)
	cmd.Dir = dir
	cmd.Env = append(append(append([]string{}, r.env...), extra...), "PWD="+dir)
	var buf limitedBuffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	configureCmd(cmd)

	start := time.Now()
	err := cmd.Run()
	res := execResult{Output: buf.buf.Bytes(), Took: time.Since(start)}
	if buf.truncated {
		res.Output = append(res.Output, "\n[output truncated]\n"...)
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case ctx.Err() != nil:
		res.Err = ctx.Err()
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		res.TimedOut = true
		res.Exit = -1
	case errors.As(err, &ee):
		res.Exit = ee.ExitCode()
	default:
		res.Err = err
	}
	return res
}

// record is one executed command block, as written by -json.
type record struct {
	Block      int    `json:"block"`
	Line       int    `json:"line"`
	Stage      string `json:"stage"`
	Command    string `json:"command"`
	Hidden     bool   `json:"hidden,omitempty"`
	Check      bool   `json:"check,omitempty"`
	ExpectExit int    `json:"expect_exit"`
	Exit       int    `json:"exit"`
	Output     string `json:"output"`
	OK         bool   `json:"ok"`
}

// failure is one reason a page does not pass.
type failure struct {
	Line   int // 1-based line of the block in the page
	Block  int
	Stage  string // "main", "solution" or "deep-dive"
	Title  string
	Detail []string
}

// pageResult is everything that happened to one page.
type pageResult struct {
	Page     *page
	Skipped  string // why the page did not run, when it did not
	Records  []record
	Failures []failure
	Commands int
	Files    int
	Rewrote  int // out blocks rewritten (-update)
	Added    int // out blocks added (-update)
	Removed  int // out blocks removed (-update)
	Err      error
	Kept     string // workspace root kept for inspection (-keep)
}

func (r *pageResult) failed() bool {
	return r.Err != nil || len(r.Failures) > 0 || len(r.Page.Errors) > 0
}

// pageRun is the state of running one page.
type pageRun struct {
	r    *runner
	p    *page
	ctx  context.Context
	root string // temporary directory of the page
	ws   string // the page workspace
	norm *normalizer
	res  *pageResult

	scope  int    // scope of the copy in use, 0 for the page workspace
	copy   string // directory of that copy, "" when not made yet
	copies int
	edits  []edit

	env  []string          // variables added to every command: services, then the page's env
	vars map[string]string // service variables, for the placeholders
}

// runPage runs the blocks of p in a new workspace and, in update mode,
// rewrites its out blocks.
func (r *runner) runPage(ctx context.Context, p *page) *pageResult {
	res := &pageResult{Page: p}
	switch {
	case len(p.Errors) > 0:
		return res
	case !p.FM.Capture:
		res.Skipped = "capture: false"
		return res
	case !p.runnable():
		res.Skipped = "no run blocks"
		return res
	case ctx.Err() != nil:
		res.Err = ctx.Err()
		return res
	}
	if r.cfg.Update && p.CRLF {
		res.Err = errors.New("-update does not rewrite files with CRLF line endings")
		return res
	}

	root, err := os.MkdirTemp("", "capture-")
	if err != nil {
		res.Err = err
		return res
	}
	root = realPath(root)
	if r.cfg.Keep {
		res.Kept = root
	} else {
		defer removeAll(root)
	}
	pr := &pageRun{r: r, p: p, ctx: ctx, root: root, ws: filepath.Join(root, "ws"), res: res}
	if err := os.Mkdir(pr.ws, 0o755); err != nil {
		res.Err = err
		return res
	}
	pr.norm = newNormalizer(r.home, r.tmp, p.FM.Normalize)
	if r.pravoHome != "" {
		pr.norm.add(r.pravoHome, "~/.pravo")
	}
	pr.norm.add(root, "<tmp>")
	pr.norm.addWorkspace(pr.ws)

	if len(p.FM.Services) > 0 {
		stop, err := pr.startServices()
		if err != nil {
			res.Err = err
			return res
		}
		defer stop()
	}
	pr.setEnv()

	for _, b := range p.Blocks {
		if b.Mode == modeIgnore {
			continue
		}
		if err := pr.step(b); err != nil {
			res.Err = err
			break
		}
	}
	pr.dropCopy()
	if res.Err == nil && r.cfg.Update && len(pr.edits) > 0 {
		if err := pr.applyEdits(); err != nil {
			res.Err = err
		}
	}
	return res
}

// step executes one block in the workspace it belongs to.
func (pr *pageRun) step(b *block) error {
	switch {
	case b.File != "":
		dir, err := pr.dirFor(b)
		if err != nil {
			return err
		}
		return pr.writeFile(b, dir)
	case b.isCommand():
		dir, err := pr.dirFor(b)
		if err != nil {
			return err
		}
		return pr.runBlock(b, dir)
	}
	return nil
}

// dirFor returns the directory b runs in. Blocks of one ::solution part or
// one deep-dive share a copy of the page workspace, made when the first of
// them needs it and removed when the part ends.
func (pr *pageRun) dirFor(b *block) (string, error) {
	if b.Scope != pr.scope {
		pr.dropCopy()
		pr.scope = b.Scope
	}
	if b.Mode == modeMain {
		return pr.ws, nil
	}
	if pr.copy == "" {
		pr.copies++
		dst := filepath.Join(pr.root, fmt.Sprintf("copy%d", pr.copies))
		if err := copyTree(pr.ws, dst); err != nil {
			return "", err
		}
		pr.norm.addWorkspace(dst)
		pr.copy = dst
	}
	return pr.copy, nil
}

func (pr *pageRun) dropCopy() {
	if pr.copy != "" && !pr.r.cfg.Keep {
		removeAll(pr.copy)
	}
	pr.copy = ""
}

// removeAll removes a directory tree, including the read-only directories
// that CUE makes when it extracts a module.
func removeAll(dir string) {
	if os.RemoveAll(dir) == nil {
		return
	}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if d != nil && d.IsDir() {
			os.Chmod(p, 0o700)
		}
		return nil
	})
	os.RemoveAll(dir)
}

// copyTree copies the directory src to dst, keeping modes and symbolic links.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case d.Type().IsRegular():
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, info.Mode().Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
		return nil
	})
}

// writeFile writes a file= block into dir. A name written twice is replaced.
func (pr *pageRun) writeFile(b *block, dir string) error {
	target := filepath.Join(dir, filepath.FromSlash(b.File))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	data := strings.Join(b.Content, "\n")
	if len(b.Content) > 0 {
		data += "\n"
	}
	data = expandHolders(data, pr.vars)
	if err := os.WriteFile(target, []byte(data), 0o644); err != nil {
		return err
	}
	pr.res.Files++
	if pr.r.cfg.Verbose {
		fmt.Fprintf(pr.r.cfg.Log, "capture: %s:%d: block %d wrote %s\n", pr.p.Path, b.Open+1, b.N, b.File)
	}
	return nil
}

// runBlock runs a command block and compares, or in update mode adopts, its
// output.
func (pr *pageRun) runBlock(b *block, dir string) error {
	script := b.script()
	if reInit.MatchString(script) {
		pr.r.initMu.Lock()
		defer pr.r.initMu.Unlock()
		unlock, err := lockFile(pr.ctx, pr.r.initLock)
		if err != nil {
			return err
		}
		defer unlock()
	}
	// A visible block runs as it would in the reader's shell: every line runs.
	// Setup and check blocks must succeed as a whole, so they stop at the
	// first failing command.
	ex := pr.r.exec(pr.ctx, dir, script, b.Hidden || b.Check, pr.env)
	if ex.Err != nil {
		if pr.ctx.Err() != nil {
			return pr.ctx.Err()
		}
		return fmt.Errorf("%s:%d: cannot run the command: %w", pr.p.Path, b.Open+1, ex.Err)
	}
	pr.res.Commands++
	actual := pr.norm.lines(ex.Output)
	wantExit := 0
	if b.HasExit && !b.Check {
		wantExit = b.Exit
	}
	rec := record{
		Block: b.N, Line: b.Open + 1, Stage: b.Mode.stage(), Command: strings.TrimSpace(script),
		Hidden: b.Hidden, Check: b.Check, ExpectExit: wantExit, Exit: ex.Exit,
		Output: strings.Join(actual, "\n"), OK: true,
	}
	if pr.r.cfg.Verbose {
		fmt.Fprintf(pr.r.cfg.Log, "capture: %s:%d: block %d [%s] %s -> exit %d (%s)\n",
			pr.p.Path, b.Open+1, b.N, b.Mode.stage(), firstLine(script), ex.Exit, ex.Took.Round(time.Millisecond))
	}

	switch {
	case ex.TimedOut:
		rec.OK = false
		pr.fail(b, fmt.Sprintf("the command did not finish within %s", pr.r.cfg.Timeout), append(commandLines(b), "output so far:"), actual)
	case ex.Exit != wantExit:
		rec.OK = false
		title := fmt.Sprintf("exit status %d, expected %d", ex.Exit, wantExit)
		detail := commandLines(b)
		switch {
		case b.Check:
			detail = append(detail, "a check block must exit 0")
		case wantExit == 0:
			detail = append(detail, fmt.Sprintf("if the failure is the point of the example, write exit=%d on the run block", ex.Exit))
		}
		detail = append(detail, "output:")
		pr.fail(b, title, detail, actual)
	}

	// Output is compared for every visible run block, and for any block that
	// has an out block. Hidden and check blocks without one are not.
	if !ex.TimedOut && (b.Pair != nil || (!b.Hidden && !b.Check)) {
		var exp []string
		if b.Pair != nil {
			exp = trimLines(b.Pair.Content)
		}
		if mm := matchElided(exp, actual); mm != nil {
			if pr.r.cfg.Update {
				pr.planEdit(b, exp, actual)
			} else {
				rec.OK = false
				pr.failOutput(b, exp, actual, mm)
			}
		}
	}
	pr.res.Records = append(pr.res.Records, rec)
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " ..."
	}
	return s
}

func commandLines(b *block) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(b.script()), "\n") {
		out = append(out, "$ "+l)
	}
	return out
}

// maxShown bounds how much output a failure prints.
const maxShown = 40

func shown(lines []string, indent string) []string {
	var out []string
	if len(lines) > maxShown {
		for _, l := range lines[:maxShown/2] {
			out = append(out, indent+l)
		}
		out = append(out, fmt.Sprintf("%s... %d lines not shown", indent, len(lines)-maxShown+maxShown/4))
		for _, l := range lines[len(lines)-maxShown/4:] {
			out = append(out, indent+l)
		}
		return out
	}
	for _, l := range lines {
		out = append(out, indent+l)
	}
	if len(lines) == 0 {
		out = append(out, indent+"(no output)")
	}
	return out
}

func (pr *pageRun) fail(b *block, title string, detail, output []string) {
	f := failure{Line: b.Open + 1, Block: b.N, Stage: b.Mode.stage(), Title: title, Detail: detail}
	f.Detail = append(f.Detail, shown(output, "  ")...)
	pr.res.Failures = append(pr.res.Failures, f)
}

// failOutput reports an output that differs from the out block: where, what
// was expected, what came out, and a diff.
func (pr *pageRun) failOutput(b *block, exp, act []string, mm *mismatch) {
	f := failure{Line: b.Open + 1, Block: b.N, Stage: b.Mode.stage()}
	f.Detail = commandLines(b)
	if b.Pair == nil {
		f.Title = "the run block prints output but has no out block"
		f.Detail = append(f.Detail, "output:")
		f.Detail = append(f.Detail, shown(act, "  ")...)
		f.Detail = append(f.Detail, "run with -update to add the out block")
		pr.res.Failures = append(pr.res.Failures, f)
		return
	}
	outLine := b.Pair.Open + 1
	f.Title = fmt.Sprintf("output differs from the out block at line %d", outLine)
	f.Detail = append(f.Detail, mm.Msg)
	line := func(lines []string, i int) string {
		if i < 0 || i >= len(lines) {
			return "(nothing)"
		}
		return lines[i]
	}
	switch {
	case mm.ExpLine >= 0:
		f.Detail = append(f.Detail, fmt.Sprintf("  expected (page line %d): %s", outLine+1+mm.ExpLine, line(exp, mm.ExpLine)))
	case mm.ActLine >= 0:
		f.Detail = append(f.Detail, "  expected: (nothing)")
	}
	switch {
	case mm.NoActual:
	case mm.ActLine >= 0:
		f.Detail = append(f.Detail, fmt.Sprintf("  actual   (output line %d): %s", mm.ActLine+1, line(act, mm.ActLine)))
	case mm.ExpLine >= 0:
		f.Detail = append(f.Detail, "  actual: (nothing)")
	}
	if len(segments(exp)) == 1 {
		f.Detail = append(f.Detail, "diff (- expected, + actual):")
		for _, l := range renderDiff(lineDiff(exp, act), 2) {
			f.Detail = append(f.Detail, "  "+l)
		}
	} else {
		f.Detail = append(f.Detail, "output:")
		f.Detail = append(f.Detail, shown(act, "  ")...)
	}
	pr.res.Failures = append(pr.res.Failures, f)
}

// editKind says what -update does to the page.
type editKind int

const (
	editReplace editKind = iota
	editInsert
	editRemove
)

// edit is one change to an out block.
type edit struct {
	kind  editKind
	run   *block
	out   *block
	lines []string
}

func (e edit) at() int {
	if e.kind == editInsert {
		return e.run.Close
	}
	return e.out.Open
}

// planEdit records how an out block must change to match actual.
func (pr *pageRun) planEdit(b *block, exp, actual []string) {
	switch {
	case b.Pair != nil && len(actual) == 0:
		pr.edits = append(pr.edits, edit{kind: editRemove, run: b, out: b.Pair})
		pr.res.Removed++
	case b.Pair != nil:
		pr.edits = append(pr.edits, edit{kind: editReplace, run: b, out: b.Pair, lines: rewrite(exp, actual)})
		pr.res.Rewrote++
	default:
		pr.edits = append(pr.edits, edit{kind: editInsert, run: b, lines: actual})
		pr.res.Added++
	}
}

// startServices starts the services the page lists and returns the function
// that stops them. The pages that name the same fixed port wait for each
// other here. A service that cannot start fails the page.
func (pr *pageRun) startServices() (stop func(), err error) {
	var fixed []int
	for _, spec := range pr.p.FM.Services {
		if spec.Port != 0 {
			fixed = append(fixed, spec.Port)
		}
	}
	release, err := pr.r.gate.acquire(pr.ctx, fixed)
	if err != nil {
		return nil, err
	}
	var started []*service
	stop = func() {
		for i := len(started) - 1; i >= 0; i-- {
			started[i].stop()
			if pr.r.cfg.Verbose {
				fmt.Fprintf(pr.r.cfg.Log, "capture: %s: stopped %s\n", pr.p.Path, started[i].spec.Name)
			}
		}
		release()
	}
	pr.vars = map[string]string{}
	for _, spec := range pr.p.FM.Services {
		bin := pr.r.services[spec.Name]
		if bin == "" {
			stop()
			return nil, fmt.Errorf("no executable for the service %s", spec.Name)
		}
		if knownServices[spec.Name].Tool {
			// A tool is not started: pravo starts it. The page gets its path
			// and a directory of its own for the objects the tool keeps.
			dir := filepath.Join(pr.ws, toolDir)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				stop()
				return nil, err
			}
			for _, kv := range toolVars(spec.Name, bin, dir) {
				pr.vars[kv[0]] = kv[1]
				pr.env = append(pr.env, kv[0]+"="+kv[1])
			}
			continue
		}
		svc, err := startService(pr.ctx, bin, spec)
		if err != nil {
			stop()
			if pr.ctx.Err() != nil {
				return nil, pr.ctx.Err()
			}
			what := spec.Name
			if spec.Port != 0 {
				what += " on " + net.JoinHostPort(serviceHost, strconv.Itoa(spec.Port))
			}
			return nil, fmt.Errorf("cannot start %s: %w", what, err)
		}
		started = append(started, svc)
		if pr.r.cfg.Verbose {
			fmt.Fprintf(pr.r.cfg.Log, "capture: %s: started %s on %s\n", pr.p.Path, spec.Name, svc.addr)
		}
		for _, kv := range serviceVars(spec.Name, svc.addr, svc.cmd.Process.Pid) {
			pr.vars[kv[0]] = kv[1]
			pr.env = append(pr.env, kv[0]+"="+kv[1])
		}
		if spec.Port == 0 {
			_, port, _ := net.SplitHostPort(svc.addr)
			n, _ := strconv.Atoi(port)
			pr.norm.addPort(spec.Name, n)
		}
	}
	return stop, nil
}

// setEnv adds to the environment of the commands what the page asks for:
// a CUE cache of its own when it has services, so that a module cached from an
// earlier registry at the same address cannot be mistaken for the one the
// page publishes, then the env map of the front matter.
func (pr *pageRun) setEnv() {
	if len(pr.p.FM.Services) > 0 {
		pr.env = append(pr.env, "CUE_CACHE_DIR="+filepath.Join(pr.root, "cue-cache"))
	}
	for _, e := range pr.p.FM.Env {
		pr.env = append(pr.env, e.Name+"="+expandHolders(e.Value, pr.vars))
	}
}

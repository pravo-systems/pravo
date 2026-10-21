package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A service is a program that a page needs while it runs, such as the
// in-memory S3 server. The page names it in its front matter; capture starts
// a fresh one before the first block of the page and stops it after the last.
//
// The contract with a service executable:
//
//   - it is started as NAME -addr 127.0.0.1:PORT -exit-on-eof, with a pipe on
//     standard input, in a process group of its own;
//   - once it accepts connections it prints "listening on ADDR" on standard
//     output, where ADDR is the address it bound (the port is the real one
//     when PORT was 0);
//   - it exits when standard input reaches end of file, so that a capture that
//     dies cannot leave it behind. capture also kills the process group.

// serviceDef describes a service that capture knows how to start.
type serviceDef struct {
	Name string // the name used in front matter and as the executable name
	Pkg  string // the Go package that is built when -services is not given
}

// Prefix is the start of the environment variables the service exports.
func (d serviceDef) Prefix() string { return strings.ToUpper(d.Name) }

// knownServices are the services a page may name.
var knownServices = map[string]serviceDef{
	"fakes3":  {Name: "fakes3", Pkg: "./tools/fakes3"},
	"fakeoci": {Name: "fakeoci", Pkg: "./tools/fakeoci"},
}

func serviceNames() []string {
	var names []string
	for n := range knownServices {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// serviceSpec is one entry of the services list of a page.
type serviceSpec struct {
	Name string
	Port int // 0 when the page lets capture choose a free port
}

// serviceHost is the address every service listens on.
const serviceHost = "127.0.0.1"

// parseServiceSpec reads "fakes3" or "fakes3:9000".
func parseServiceSpec(s string) (serviceSpec, error) {
	name, port, hasPort := strings.Cut(strings.TrimSpace(s), ":")
	if _, ok := knownServices[name]; !ok {
		return serviceSpec{}, fmt.Errorf("unknown service %q (known: %s)", name, strings.Join(serviceNames(), ", "))
	}
	spec := serviceSpec{Name: name}
	if hasPort {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return serviceSpec{}, fmt.Errorf("service %s: %q is not a port from 1 to 65535", name, port)
		}
		spec.Port = n
	}
	return spec, nil
}

func (s serviceSpec) String() string {
	if s.Port == 0 {
		return s.Name
	}
	return fmt.Sprintf("%s:%d", s.Name, s.Port)
}

// reHolder matches a placeholder such as {{FAKES3_PORT}}. Only the variables
// of the known services are placeholders; any other {{text}} is left alone.
var reHolder = regexp.MustCompile(`\{\{([A-Z][A-Z0-9]*)_(ADDR|URL|PORT)\}\}`)

// holderService returns the service a placeholder name belongs to, or "".
func holderService(prefix string) string {
	for n, d := range knownServices {
		if d.Prefix() == prefix {
			return n
		}
	}
	return ""
}

// serviceVars returns the variables a running service exports: NAME_ADDR
// (127.0.0.1:PORT), NAME_URL (http://127.0.0.1:PORT), NAME_PORT and NAME_PID,
// the process that a page can stop with kill.
func serviceVars(name, addr string, pid int) [][2]string {
	prefix := knownServices[name].Prefix()
	_, port, _ := net.SplitHostPort(addr)
	return [][2]string{
		{prefix + "_ADDR", addr},
		{prefix + "_URL", "http://" + addr},
		{prefix + "_PORT", port},
		{prefix + "_PID", strconv.Itoa(pid)},
	}
}

// expandHolders replaces the placeholders of vars in s.
func expandHolders(s string, vars map[string]string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	return reHolder.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := vars[strings.Trim(m, "{}")]; ok {
			return v
		}
		return m
	})
}

// Timing of the start of a service. Variables so that tests can shorten them.
var (
	serviceStartTimeout = 30 * time.Second // from the start of the process to "listening on"
	serviceStopTimeout  = 10 * time.Second // from the kill to the exit of the process
	servicePortRetries  = 10               // tries to bind a fixed port that another program holds
	servicePortWait     = 300 * time.Millisecond
)

// service is a running service.
type service struct {
	spec   serviceSpec
	addr   string // 127.0.0.1:PORT, with the port that was bound
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stdin  io.WriteCloser
	done   chan struct{} // closed when the process has exited and been waited for
	out    *lockedBuffer
	errOut *lockedBuffer
}

// lockedBuffer is a bytes.Buffer that one goroutine writes while another reads.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	// line receives the address of the first "listening on ADDR" line.
	line chan string
	sent bool
}

const maxServiceOutput = 1 << 20

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.buf.Len() < maxServiceOutput {
		b.buf.Write(p)
	}
	if b.line != nil && !b.sent {
		rest := b.buf.String()
		for {
			i := strings.IndexByte(rest, '\n')
			if i < 0 {
				break
			}
			l := strings.TrimSpace(rest[:i])
			rest = rest[i+1:]
			if addr, ok := strings.CutPrefix(l, "listening on "); ok {
				b.sent = true
				b.line <- addr
				break
			}
		}
	}
	return len(p), nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// tail returns the last few non-empty lines, indented for a report.
func tail(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, " \t\r"); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// startService starts bin for spec and waits until it accepts connections.
// A fixed port that another program holds is tried again a few times.
func startService(ctx context.Context, bin string, spec serviceSpec) (*service, error) {
	var lastErr error
	inUse := false
	for attempt := 0; attempt < max(1, servicePortRetries); attempt++ {
		svc, retry, err := tryStartService(ctx, bin, spec)
		if err == nil {
			return svc, nil
		}
		lastErr, inUse = err, retry
		if !retry || spec.Port == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(servicePortWait):
		}
	}
	if inUse && spec.Port != 0 {
		lastErr = fmt.Errorf("%w\n      another program holds port %d", lastErr, spec.Port)
	}
	return nil, lastErr
}

// tryStartService makes one attempt. retry is true when the service exited
// because the port is in use.
func tryStartService(ctx context.Context, bin string, spec serviceSpec) (svc *service, retry bool, err error) {
	addr := net.JoinHostPort(serviceHost, strconv.Itoa(spec.Port))
	sctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(sctx, bin, "-addr", addr, "-exit-on-eof")
	configureCmd(cmd)
	out := &lockedBuffer{line: make(chan string, 1)}
	errOut := &lockedBuffer{}
	cmd.Stdout, cmd.Stderr = out, errOut
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, false, fmt.Errorf("cannot start %s: %w", spec.Name, err)
	}
	s := &service{spec: spec, cmd: cmd, cancel: cancel, stdin: stdin, done: make(chan struct{}), out: out, errOut: errOut}
	go func() {
		cmd.Wait()
		close(s.done)
	}()

	fail := func(format string, args ...any) (*service, bool, error) {
		s.stop()
		msg := fmt.Sprintf(format, args...)
		if o := tail(errOut.String()+"\n"+out.String(), 5); o != "" {
			msg += "\n" + indent(o, "      ")
		}
		return nil, strings.Contains(errOut.String(), "in use") || strings.Contains(errOut.String(), "Only one usage"), errors.New(msg)
	}

	timer := time.NewTimer(serviceStartTimeout)
	defer timer.Stop()
	select {
	case got := <-out.line:
		s.addr = got
	case <-s.done:
		return fail("%s exited before it was ready", spec.Name)
	case <-timer.C:
		return fail("%s did not print \"listening on ADDR\" within %s", spec.Name, serviceStartTimeout)
	case <-ctx.Done():
		s.stop()
		return nil, false, ctx.Err()
	}
	if host, port, err := net.SplitHostPort(s.addr); err != nil || port == "0" || host == "" {
		return fail("%s announced the address %q", spec.Name, s.addr)
	}
	if spec.Port != 0 && !strings.HasSuffix(s.addr, ":"+strconv.Itoa(spec.Port)) {
		return fail("%s was asked to listen on port %d and announced %s", spec.Name, spec.Port, s.addr)
	}
	// "listening on" says the port is bound; connecting says it is served.
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, derr := net.DialTimeout("tcp", s.addr, time.Second)
		if derr == nil {
			c.Close()
			break
		}
		select {
		case <-s.done:
			return fail("%s exited before it accepted a connection", spec.Name)
		default:
		}
		if time.Now().After(deadline) {
			return fail("%s does not accept connections on %s: %v", spec.Name, s.addr, derr)
		}
		select {
		case <-ctx.Done():
			s.stop()
			return nil, false, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return s, false, nil
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

// stop ends the service and every process it started, and waits for it. The
// group is killed here and not left to the context of the command, because a
// service that exits when its standard input closes may be gone before the
// context is looked at, and the helpers it started would stay.
func (s *service) stop() {
	s.stdin.Close()
	killGroup(s.cmd)
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(serviceStopTimeout):
	}
}

// buildServices builds the executables of the named services once, into a new
// directory, and returns it. The Go packages are in the module at root.
func buildServices(ctx context.Context, root string, names []string) (dir string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", "capture-services-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { os.RemoveAll(dir) }
	args := []string{"build", "-o", dir + string(filepath.Separator)}
	for _, n := range names {
		args = append(args, knownServices[n].Pkg)
	}
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = root
	if out, berr := cmd.CombinedOutput(); berr != nil {
		cleanup()
		return "", nil, fmt.Errorf("building %s: %v\n%s", strings.Join(names, ", "), berr, strings.TrimSpace(string(out)))
	}
	return dir, cleanup, nil
}

// serviceExecutable returns the path of the executable of a service in dir.
func serviceExecutable(dir, name string) (string, error) {
	for _, n := range []string{name, name + ".exe"} {
		p := filepath.Join(dir, n)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("-services %s: no executable named %s", dir, name)
}

// lockDir is where the lock files of the ports and of pravo init live. A
// variable so that the tests can keep theirs apart.
var lockDir = os.TempDir

// portGate makes pages that name the same fixed port run one after another:
// inside this process with a channel per port, and between capture processes
// with a lock file per port, because two servers cannot hold one port.
type portGate struct {
	mu    sync.Mutex
	ports map[int]chan struct{}
}

func (g *portGate) slot(port int) chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ports == nil {
		g.ports = map[int]chan struct{}{}
	}
	if g.ports[port] == nil {
		g.ports[port] = make(chan struct{}, 1)
	}
	return g.ports[port]
}

// acquire takes the given ports, lowest first so that two pages that name the
// same ports in a different order cannot wait for each other, and returns the
// function that gives them back.
func (g *portGate) acquire(ctx context.Context, ports []int) (release func(), err error) {
	ports = append([]int(nil), ports...)
	sort.Ints(ports)
	var undo []func()
	release = func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}
	for _, port := range ports {
		slot := g.slot(port)
		select {
		case slot <- struct{}{}:
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		}
		undo = append(undo, func() { <-slot })
		unlock, lerr := lockFile(ctx, filepath.Join(lockDir(), fmt.Sprintf("capture-port-%d.lock", port)))
		if lerr != nil {
			release()
			return nil, lerr
		}
		undo = append(undo, unlock)
	}
	return release, nil
}

// neededServices lists, sorted, the services of the pages that will run.
func neededServices(pages []*page) []string {
	seen := map[string]bool{}
	for _, p := range pages {
		if !p.FM.Capture || len(p.Errors) > 0 || !p.runnable() {
			continue
		}
		for _, s := range p.FM.Services {
			seen[s.Name] = true
		}
	}
	var names []string
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// prepareServices finds the executable of each named service: in dir when it
// is given, otherwise in a directory where the services are built, once, from
// the module that contains the current directory or one of starts.
func prepareServices(ctx context.Context, dir string, names, starts []string, verbose bool, log io.Writer) (bins map[string]string, cleanup func(), err error) {
	cleanup = func() {}
	if dir == "" {
		root, rerr := findModuleRoot(append([]string{"."}, starts...)...)
		if rerr != nil {
			return nil, cleanup, fmt.Errorf("the pages list services (%s): %w, or pass -services DIR with the executables", strings.Join(names, ", "), rerr)
		}
		if verbose {
			fmt.Fprintf(log, "capture: building %s from %s\n", strings.Join(names, ", "), root)
		}
		dir, cleanup, err = buildServices(ctx, root, names)
		if err != nil {
			return nil, func() {}, err
		}
	}
	bins = map[string]string{}
	for _, n := range names {
		bin, berr := serviceExecutable(dir, n)
		if berr != nil {
			cleanup()
			return nil, func() {}, berr
		}
		bins[n] = bin
	}
	return bins, cleanup, nil
}

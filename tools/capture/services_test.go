//go:build unix

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The tests of the services run against a stand-in. The test binary is the
// stand-in: TestMain looks at the name it was started under, and the tests
// put links named fakes3, fakeoci and httpget to the test binary in a
// directory. Nothing is built, and every service starts in milliseconds.
//
//	fakes3, fakeoci  a server with the contract of tools/fakes3
//	httpget URL      prints the body of a GET, a small curl
//	capture          the capture command itself, for the tests that send it a signal
func TestMain(m *testing.M) {
	switch filepath.Base(os.Args[0]) {
	case "fakes3", "fakeoci":
		os.Exit(standIn(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	case "httpget":
		os.Exit(httpGet(os.Args[1:], os.Stdout, os.Stderr))
	case "capture":
		main()
		return
	}
	// The lock files of the ports and of pravo init stay out of the shared
	// temporary directory.
	dir, err := os.MkdirTemp("", "capture-test-locks-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	lockDir = func() string { return dir }
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// standIn serves "<name> stand-in" to every request. STANDIN_MODE changes what
// it does: "exit" fails at once, "silent" binds and never says so, "wrongport"
// announces another port, "slow" says it listens before it does.
// STANDIN_PIDFILE receives a line "<name> <pid> <port>" when it is ready.
func standIn(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	name := filepath.Base(os.Args[0])
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	addr := flags.String("addr", "127.0.0.1:0", "")
	exitOnEOF := flags.Bool("exit-on-eof", false, "")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	mode := os.Getenv("STANDIN_MODE")
	if mode == "exit" {
		fmt.Fprintf(stderr, "%s: boom\n", name)
		return 1
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s stand-in\n", name)
	}))
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	if f := os.Getenv("STANDIN_PIDFILE"); f != "" {
		if fh, err := os.OpenFile(f, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(fh, "%s %d %s\n", name, os.Getpid(), port)
			fh.Close()
		}
	}
	switch mode {
	case "silent":
	case "wrongport":
		fmt.Fprintln(stdout, "listening on 127.0.0.1:1")
	default:
		fmt.Fprintf(stdout, "listening on %s\n", ln.Addr())
	}
	if *exitOnEOF {
		io.Copy(io.Discard, stdin)
		return 0
	}
	select {}
}

func httpGet(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: httpget URL")
		return 2
	}
	c := http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get(args[0])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer resp.Body.Close()
	io.Copy(stdout, resp.Body)
	return 0
}

// standInDir makes the directory of the stand-ins and puts it first on PATH,
// so that the commands of a page find httpget. It returns the directory, which
// is also what -services takes.
func standInDir(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"fakes3", "fakeoci", "httpget", "capture"} {
		if err := os.Symlink(exe, filepath.Join(dir, name)); err != nil {
			t.Skipf("symbolic links: %v", err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// shortTimes makes the waits of the services short for one test.
func shortTimes(t *testing.T) {
	t.Helper()
	start, retries, wait := serviceStartTimeout, servicePortRetries, servicePortWait
	serviceStartTimeout, servicePortRetries, servicePortWait = 400*time.Millisecond, 3, 50*time.Millisecond
	t.Cleanup(func() { serviceStartTimeout, servicePortRetries, servicePortWait = start, retries, wait })
}

// freePort returns a port that nothing listens on.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

const head = "---\ntitle: T\ndescription: D\norder: 1\n"

// servicePage is a page with the given front matter lines and body.
func servicePage(t *testing.T, name, front, body string) string {
	t.Helper()
	return writePage(t, name, head+front+"---\n\n"+body)
}

func run1(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	return capture(t, append([]string{"-bin", fakeBin(t), "-services", dir}, args...)...)
}

// pids reads the pid file of the stand-ins: "<name> <pid> <port>" per line.
func pids(t *testing.T, file string) [][3]string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatal(err)
	}
	var out [][3]string
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		f := strings.Fields(l)
		if len(f) == 3 {
			out = append(out, [3]string{f[0], f[1], f[2]})
		}
	}
	return out
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// noneLeft fails the test when a stand-in in the pid file is still running or
// still holds its port.
func noneLeft(t *testing.T, file string, want int) {
	t.Helper()
	got := pids(t, file)
	if len(got) != want {
		t.Fatalf("%d stand-ins started, want %d (%v)", len(got), want, got)
	}
	for _, g := range got {
		pid, _ := strconv.Atoi(g[1])
		if alive(pid) {
			t.Errorf("%s (pid %d) is still running", g[0], pid)
		}
		if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", g[2]), 200*time.Millisecond); err == nil {
			c.Close()
			t.Errorf("%s still accepts connections on port %s", g[0], g[2])
		}
	}
}

func TestServicesOnFreePortsAreReachableAndStopped(t *testing.T) {
	dir := standInDir(t)
	pidfile := filepath.Join(t.TempDir(), "pids")
	t.Setenv("STANDIN_PIDFILE", pidfile)
	page := servicePage(t, "free.md", "services:\n  - fakes3\n  - fakeoci\n", ""+
		"```text file=conf.txt\ns3 {{FAKES3_URL}}\nregistry {{FAKEOCI_ADDR}} port {{FAKEOCI_PORT}}\n{{OTHER}} {{ FAKES3_PORT }} {{NOSERVICE_PORT}}\n```\n\n"+
		"```sh run\ncat conf.txt\nhttpget \"$FAKES3_URL/\"\nhttpget \"http://$FAKEOCI_ADDR/\"\ntest \"$FAKES3_PORT\" != \"$FAKEOCI_PORT\"\necho \"$FAKES3_ADDR\"\necho \"$FAKEOCI_URL\"\n```\n\n"+
		"```text out\ns3 http://127.0.0.1:<fakes3-port>\nregistry 127.0.0.1:<fakeoci-port> port <fakeoci-port>\n{{OTHER}} {{ FAKES3_PORT }} {{NOSERVICE_PORT}}\nfakes3 stand-in\nfakeoci stand-in\n127.0.0.1:<fakes3-port>\nhttp://127.0.0.1:<fakeoci-port>\n```\n")
	code, out, errb := run1(t, dir, page)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	noneLeft(t, pidfile, 2)

	// The two services were given different ports, and the page saw neither.
	got := pids(t, pidfile)
	if got[0][2] == got[1][2] || got[0][2] == "0" {
		t.Errorf("ports = %s and %s", got[0][2], got[1][2])
	}
}

func TestServicesAreStartedFreshForEveryPage(t *testing.T) {
	dir := standInDir(t)
	pidfile := filepath.Join(t.TempDir(), "pids")
	t.Setenv("STANDIN_PIDFILE", pidfile)
	one := servicePage(t, "one.md", "services: fakes3\n", "```sh run\nhttpget \"$FAKES3_URL\"\n```\n\n```text out\nfakes3 stand-in\n```\n")
	two := servicePage(t, "two.md", "services: [fakes3]\n", "```sh run\nhttpget \"$FAKES3_URL\"\n```\n\n```text out\nfakes3 stand-in\n```\n")
	code, out, errb := run1(t, dir, one, two)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	noneLeft(t, pidfile, 2)
	if got := pids(t, pidfile); got[0][1] == got[1][1] {
		t.Errorf("the two pages used one process: %v", got)
	}
}

func TestServiceOnANamedPort(t *testing.T) {
	dir := standInDir(t)
	port := freePort(t)
	pidfile := filepath.Join(t.TempDir(), "pids")
	t.Setenv("STANDIN_PIDFILE", pidfile)
	page := servicePage(t, "fixed.md", fmt.Sprintf("services:\n  - fakes3:%d\n", port), fmt.Sprintf(""+
		"```text file=conf.txt\nendpoint http://127.0.0.1:%[1]d port {{FAKES3_PORT}}\n```\n\n"+
		"```sh run\ncat conf.txt\nhttpget http://127.0.0.1:%[1]d/\necho $FAKES3_ADDR\n```\n\n"+
		"```text out\nendpoint http://127.0.0.1:%[1]d port %[1]d\nfakes3 stand-in\n127.0.0.1:%[1]d\n```\n", port))
	code, out, errb := run1(t, dir, page)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	noneLeft(t, pidfile, 1)
	if got := pids(t, pidfile); got[0][2] != strconv.Itoa(port) {
		t.Errorf("the service listened on %s, want %d", got[0][2], port)
	}
}

func TestPagesThatNameOnePortRunOneAfterAnother(t *testing.T) {
	dir := standInDir(t)
	// Too short to wait for the other page: only the gate keeps them apart.
	shortTimes(t)
	port := freePort(t)
	markers := t.TempDir()
	t.Setenv("MARKERS", markers)
	// Each page marks that it is running, waits, and fails if the other page
	// is running at the same time. The marker is removed at the end.
	mk := func(name, me, other string) string {
		return servicePage(t, name+".md", fmt.Sprintf("services:\n  - fakes3:%d\n", port), fmt.Sprintf(""+
			"```sh run\ntouch \"$MARKERS/%s\"\nsleep 0.4\nif [ -e \"$MARKERS/%s\" ]; then echo overlap; fi\nrm \"$MARKERS/%s\"\n```\n", me, other, me))
	}
	a, b := mk("a", "a", "b"), mk("b", "b", "a")
	code, out, errb := run1(t, dir, "-j", "2", a, b)
	if code != 0 {
		t.Fatalf("exit %d: the pages ran at the same time or failed\n%s%s", code, out, errb)
	}
}

func TestPagesOnDifferentPortsRunTogether(t *testing.T) {
	// The control of the test above: nothing but the shared port keeps two
	// pages apart. Each page waits for the marker of the other, so the pages
	// pass only when both run at once.
	dir := standInDir(t)
	markers := t.TempDir()
	t.Setenv("MARKERS", markers)
	mk := func(name, me, other string) string {
		return servicePage(t, name+".md", fmt.Sprintf("services:\n  - fakes3:%d\n", freePort(t)), fmt.Sprintf(""+
			"```sh run hidden\ntouch \"$MARKERS/%s\"\nfor i in $(seq 1 100); do [ -e \"$MARKERS/%s\" ] && exit 0; sleep 0.05; done\nexit 1\n```\n", me, other))
	}
	a, b := mk("a", "a", "b"), mk("b", "b", "a")
	if code, out, errb := run1(t, dir, "-j", "2", a, b); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
}

func TestPagesNamingPortsInAnotherOrderDoNotWaitForEachOther(t *testing.T) {
	dir := standInDir(t)
	p1, p2 := freePort(t), freePort(t)
	a := servicePage(t, "a.md", fmt.Sprintf("services: [fakes3:%d, fakeoci:%d]\n", p1, p2), "```sh run\nsleep 0.2\n```\n")
	b := servicePage(t, "b.md", fmt.Sprintf("services: [fakeoci:%d, fakes3:%d]\n", p2, p1), "```sh run\nsleep 0.2\n```\n")
	done := make(chan int, 1)
	go func() {
		code, _, _ := run1(t, dir, "-j", "2", a, b)
		done <- code
	}()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d", code)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("two pages that name the same ports in another order wait for each other")
	}
}

func TestFixedPortHeldByAnotherProgram(t *testing.T) {
	dir := standInDir(t)
	shortTimes(t)
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	port := held.Addr().(*net.TCPAddr).Port
	page := servicePage(t, "held.md", fmt.Sprintf("services: [fakes3:%d]\n", port), "```sh run\necho unreachable\n```\n\n```text out\nunreachable\n```\n")
	code, out, _ := run1(t, dir, page)
	if code != 1 || !strings.Contains(out, "cannot start fakes3 on 127.0.0.1:") || !strings.Contains(out, "address already in use") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
	if strings.Contains(out, "unreachable") {
		t.Errorf("a command ran without its service:\n%s", out)
	}

	// A port that is freed while capture is trying is taken.
	go func() {
		time.Sleep(60 * time.Millisecond)
		held.Close()
	}()
	servicePortRetries = 40
	if code, out, errb := run1(t, dir, page); code != 0 {
		t.Errorf("a port that was freed in time: exit %d\n%s%s", code, out, errb)
	}
}

func TestServiceThatCannotStart(t *testing.T) {
	dir := standInDir(t)
	shortTimes(t)
	t.Setenv("STANDIN_MODE", "exit")
	page := servicePage(t, "exit.md", "services: [fakes3, fakeoci]\n", "```sh run\necho unreachable\n```\n\n```text out\nunreachable\n```\n")
	start := time.Now()
	code, out, _ := run1(t, dir, page)
	if code != 1 || !strings.Contains(out, "FAIL") || !strings.Contains(out, "cannot start fakes3: fakes3 exited before it was ready") || !strings.Contains(out, "fakes3: boom") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
	if strings.Contains(out, "unreachable") {
		t.Errorf("a command ran without its services:\n%s", out)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
}

func TestServiceThatNeverSaysItIsReady(t *testing.T) {
	dir := standInDir(t)
	shortTimes(t)
	pidfile := filepath.Join(t.TempDir(), "pids")
	t.Setenv("STANDIN_PIDFILE", pidfile)
	t.Setenv("STANDIN_MODE", "silent")
	page := servicePage(t, "silent.md", "services: [fakes3]\n", "```sh run\necho unreachable\n```\n")
	code, out, _ := run1(t, dir, page)
	if code != 1 || !strings.Contains(out, "did not print \"listening on ADDR\" within 400ms") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
	noneLeft(t, pidfile, 1)
}

func TestServiceThatAnnouncesAnotherPort(t *testing.T) {
	dir := standInDir(t)
	shortTimes(t)
	pidfile := filepath.Join(t.TempDir(), "pids")
	t.Setenv("STANDIN_PIDFILE", pidfile)
	t.Setenv("STANDIN_MODE", "wrongport")
	port := freePort(t)
	page := servicePage(t, "wrong.md", fmt.Sprintf("services: [fakes3:%d]\n", port), "```sh run\necho unreachable\n```\n")
	code, out, _ := run1(t, dir, page)
	if code != 1 || !strings.Contains(out, fmt.Sprintf("was asked to listen on port %d and announced 127.0.0.1:1", port)) {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
	noneLeft(t, pidfile, 1)
}

func TestFailingPageStopsItsServices(t *testing.T) {
	dir := standInDir(t)
	pidfile := filepath.Join(t.TempDir(), "pids")
	t.Setenv("STANDIN_PIDFILE", pidfile)
	page := servicePage(t, "fail.md", "services: [fakes3, fakeoci]\n", "```sh run\necho one\n```\n\n```text out\ntwo\n```\n")
	code, out, _ := run1(t, dir, page)
	if code != 1 || !strings.Contains(out, "FAIL") {
		t.Errorf("exit %d, output:\n%s", code, out)
	}
	noneLeft(t, pidfile, 2)
}

func TestPageEnv(t *testing.T) {
	dir := standInDir(t)
	t.Setenv("CAPTURE_TEST_VALUE", "from the caller")
	page := servicePage(t, "env.md", "services: [fakes3]\nenv:\n  CAPTURE_TEST_VALUE: from the page\n  GIT_NAME: 'a {{FAKES3_PORT}} b'\n  COUNT: 3\n", ""+
		"```sh run\necho \"$CAPTURE_TEST_VALUE\"\necho \"$GIT_NAME\"\necho $COUNT\ncase $CUE_CACHE_DIR in */cue-cache) echo private cache;; esac\n```\n\n"+
		"```text out\nfrom the page\na <fakes3-port> b\n3\nprivate cache\n```\n")
	code, out, errb := run1(t, dir, page)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}

	// The page's env wins over the private cache; a page without services has
	// no private cache and still gets its env.
	plain := servicePage(t, "plain.md", "env:\n  CUE_CACHE_DIR: /elsewhere\n  KEPT: 'yes'\n", "```sh run\necho \"$CUE_CACHE_DIR $KEPT\"\n```\n\n```text out\n/elsewhere yes\n```\n")
	if code, out, errb := capture(t, "-bin", fakeBin(t), plain); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	nocache := servicePage(t, "nocache.md", "", "```sh run\necho \"[${CUE_CACHE_DIR-unset}]\"\n```\n\n```text out\n[unset]\n```\n")
	t.Setenv("CUE_CACHE_DIR", "")
	os.Unsetenv("CUE_CACHE_DIR")
	if code, out, errb := capture(t, "-bin", fakeBin(t), nocache); code != 0 {
		t.Fatalf("a page without services has a private cache: exit %d\n%s%s", code, out, errb)
	}
}

func TestPrivateCacheWithReadOnlyDirectoriesIsRemoved(t *testing.T) {
	// CUE extracts modules into read-only directories. The page makes some
	// and records where its cache was.
	dir := standInDir(t)
	record := filepath.Join(t.TempDir(), "cache-dir")
	t.Setenv("RECORD", record)
	page := servicePage(t, "readonly.md", "services: [fakes3]\n", ""+
		"```sh run hidden\nmkdir -p \"$CUE_CACHE_DIR/mod/x\"\ntouch \"$CUE_CACHE_DIR/mod/x/file\"\nchmod -R a-w \"$CUE_CACHE_DIR/mod\"\necho \"$CUE_CACHE_DIR\" > \"$RECORD\"\n```\n")
	if code, out, errb := run1(t, dir, page); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	cache := strings.TrimSpace(string(data))
	if _, err := os.Stat(cache); err == nil {
		removeAll(filepath.Dir(cache))
		t.Errorf("the read-only cache %s was left behind", cache)
	}
}

func TestRemoveAllRemovesReadOnlyTrees(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tree")
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "a", "b", "f"), []byte("x"), 0o444)
	os.Chmod(filepath.Join(root, "a", "b"), 0o555)
	os.Chmod(filepath.Join(root, "a"), 0o555)
	removeAll(root)
	if _, err := os.Stat(root); err == nil {
		t.Error("the tree is still there")
		os.Chmod(filepath.Join(root, "a"), 0o755)
		os.Chmod(filepath.Join(root, "a", "b"), 0o755)
	}
}

func TestVerboseNamesTheServices(t *testing.T) {
	dir := standInDir(t)
	page := servicePage(t, "verbose.md", "services: [fakes3]\n", "```sh run hidden\ntrue\n```\n")
	code, _, errb := run1(t, dir, "-v", page)
	if code != 0 || !strings.Contains(errb, ": started fakes3 on 127.0.0.1:") || !strings.Contains(errb, ": stopped fakes3") {
		t.Errorf("exit %d, log:\n%s", code, errb)
	}
}

func TestServicesAreOnlyNeededByThePagesThatRun(t *testing.T) {
	// A page that is skipped, or has no run block, needs no service and no
	// directory of executables.
	skipped := servicePage(t, "skipped.md", "capture: false\nservices: [fakes3]\n", "```sh run\ntrue\n```\n")
	prose := servicePage(t, "prose.md", "services: [fakeoci]\n", "Text.\n")
	if code, out, errb := capture(t, "-bin", fakeBin(t), "-services", "/nonexistent", skipped, prose); code != 0 {
		t.Errorf("exit %d\n%s%s", code, out, errb)
	}
}

func TestMissingServiceExecutable(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "fakes3"), []byte("#!/bin/sh\n"), 0o755)
	page := servicePage(t, "missing.md", "services: [fakes3, fakeoci]\n", "```sh run\ntrue\n```\n")
	code, _, errb := capture(t, "-bin", fakeBin(t), "-services", dir, page)
	if code != 2 || !strings.Contains(errb, "no executable named fakeoci") {
		t.Errorf("exit %d, stderr %q", code, errb)
	}
	// A file that is not executable does not count.
	os.WriteFile(filepath.Join(dir, "fakeoci"), []byte("#!/bin/sh\n"), 0o644)
	if code, _, errb := capture(t, "-bin", fakeBin(t), "-services", dir, page); code != 2 || !strings.Contains(errb, "no executable named fakeoci") {
		t.Errorf("exit %d, stderr %q", code, errb)
	}
}

func TestServiceFrontMatterErrors(t *testing.T) {
	cases := []struct{ front, want string }{
		{"services: [nosuch]\n", `unknown service "nosuch" (known: fakeoci, fakes3)`},
		{"services: [fakes3:0]\n", `"0" is not a port from 1 to 65535`},
		{"services: [fakes3:http]\n", `"http" is not a port`},
		{"services: [fakes3:70000]\n", `"70000" is not a port`},
		{"services: [fakes3, fakes3:9000]\n", "service fakes3 is listed twice"},
		{"services: [fakes3:9000, fakeoci:9000]\n", "fakes3 and fakeoci name the same port 9000"},
		{"services: {fakes3: 9000}\n", "services must be a list of service names"},
		{"services: [[fakes3]]\n", "services entry 1 must be a name"},
		{"services: [3]\n", "services entry 1 must be a name"},
		{"env: [A]\n", "env must be a map"},
		{"env:\n  1BAD: x\n", `"1BAD" is not a variable name`},
		{"env:\n  PATH: x\n", "capture sets PATH itself"},
		{"env:\n  A: [x]\n", "the value of A must be a string"},
	}
	for _, c := range cases {
		p := parsePage("x.md", []byte(head+c.front+"---\n\n```sh run\ntrue\n```\n"))
		found := false
		for _, e := range p.Errors {
			if strings.Contains(e.Msg, c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: errors %v, want one with %q", c.front, p.Errors, c.want)
		}
	}
}

func TestServiceFrontMatterParses(t *testing.T) {
	p := parsePage("x.md", []byte(head+"services:\n  - fakes3:9000\n  - fakeoci\nenv:\n  B: two\n  A: 1\n---\n\n```sh run\ntrue\n```\n"))
	if len(p.Errors) != 0 {
		t.Fatal(p.Errors)
	}
	if got := fmt.Sprint(p.FM.Services); got != "[fakes3:9000 fakeoci]" {
		t.Errorf("services = %s", got)
	}
	if got := fmt.Sprint(p.FM.Env); got != "[{A 1} {B two}]" {
		t.Errorf("env = %s", got)
	}
}

func TestPlaceholderOfAServiceThePageDoesNotStart(t *testing.T) {
	src := head + "services: [fakeoci]\nenv:\n  X: '{{FAKES3_URL}}'\n---\n\n" +
		"```text file=a.txt\n{{FAKEOCI_PORT}} fine\n{{FAKES3_PORT}} not started\n```\n\n" +
		"```text title=a.txt\n{{FAKES3_PORT}} only shown\n```\n\n```sh run\ntrue\n```\n"
	p := parsePage("x.md", []byte(src))
	if len(p.Errors) != 2 {
		t.Fatalf("errors = %v, want one for the env value and one for the file block", p.Errors)
	}
	for _, e := range p.Errors {
		if !strings.Contains(e.Msg, "names the service fakes3, which the page does not list under services") {
			t.Errorf("error %q", e.Msg)
		}
	}
	if p.Errors[0].Line != 6 || p.Errors[1].Line != 12 {
		t.Errorf("error lines = %d, %d, want 6 (env) and 12", p.Errors[0].Line, p.Errors[1].Line)
	}
}

func TestExpandHolders(t *testing.T) {
	vars := map[string]string{"FAKES3_PORT": "9000", "FAKES3_ADDR": "127.0.0.1:9000"}
	cases := []struct{ in, want string }{
		{"{{FAKES3_PORT}}", "9000"},
		{"http://{{FAKES3_ADDR}}/x {{FAKES3_PORT}}", "http://127.0.0.1:9000/x 9000"},
		{"{{FAKES3_URL}}", "{{FAKES3_URL}}"},
		{"{{fakes3_port}} {{ FAKES3_PORT }} ${{ secrets.X }} {{.Port}}", "{{fakes3_port}} {{ FAKES3_PORT }} ${{ secrets.X }} {{.Port}}"},
		{"no braces", "no braces"},
	}
	for _, c := range cases {
		if got := expandHolders(c.in, vars); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestReplacePorts(t *testing.T) {
	n := &normalizer{}
	n.addPort("fakes3", 41233)
	n.addPort("fakeoci", 5078)
	cases := []struct{ in, want string }{
		{"http://127.0.0.1:41233/b", "http://127.0.0.1:<fakes3-port>/b"},
		{"41233", "<fakes3-port>"},
		{"41233 41233,41233", "<fakes3-port> <fakes3-port>,<fakes3-port>"},
		{"localhost:5078 and 127.0.0.1:41233", "localhost:<fakeoci-port> and 127.0.0.1:<fakes3-port>"},
		{"412330 141233 5078.1 sha256:50789", "412330 141233 <fakeoci-port>.1 sha256:50789"},
		{"nothing", "nothing"},
	}
	for _, c := range cases {
		if got := n.replacePorts(c.in); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestServiceLeavesWhenItsParentDies(t *testing.T) {
	dir := standInDir(t)
	bin := filepath.Join(dir, "fakes3")
	svc, err := startService(context.Background(), bin, serviceSpec{Name: "fakes3"})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.stop()
	// The parent's end of the pipe closes when the parent exits. Closing it
	// here is the same event.
	svc.stdin.Close()
	select {
	case <-svc.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the service did not exit when its standard input closed")
	}
}

func TestStopKillsTheWholeProcessGroup(t *testing.T) {
	// A service that starts a helper must not leave the helper behind. The
	// script starts a helper in its own process group, then becomes the
	// stand-in.
	dir := t.TempDir()
	exe, _ := os.Executable()
	sub := filepath.Join(dir, "real")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(sub, "fakes3")); err != nil {
		t.Skip(err)
	}
	pidfile := filepath.Join(dir, "helper.pid")
	script := fmt.Sprintf("#!/bin/sh\nsleep 300 &\necho $! > %s\nexec %s \"$@\"\n", pidfile, filepath.Join(sub, "fakes3"))
	bin := filepath.Join(dir, "fakes3")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	svc, err := startService(context.Background(), bin, serviceSpec{Name: "fakes3"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(pidfile)
	if err != nil {
		svc.stop()
		t.Fatal(err)
	}
	helper, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if !alive(helper) {
		svc.stop()
		t.Fatalf("the helper %d is not running", helper)
	}
	svc.stop()
	deadline := time.Now().Add(3 * time.Second)
	for alive(helper) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(helper) {
		syscall.Kill(helper, syscall.SIGKILL)
		t.Errorf("the helper %d of the service survived stop", helper)
	}
}

func TestInterruptStopsTheServices(t *testing.T) {
	dir := standInDir(t)
	pidfile := filepath.Join(t.TempDir(), "pids")
	started := filepath.Join(t.TempDir(), "started")
	port := freePort(t)
	page := servicePage(t, "sigint.md", fmt.Sprintf("services:\n  - fakes3\n  - fakeoci:%d\n", port),
		fmt.Sprintf("```sh run\ntouch %s\nsleep 60\n```\n", started))

	cmd := exec.Command(filepath.Join(dir, "capture"), "-bin", fakeBin(t), "-services", dir, page)
	cmd.Env = append(os.Environ(), "STANDIN_PIDFILE="+pidfile, "TMPDIR="+t.TempDir())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var buf lockedBuffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}()

	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the page did not start its command:\n%s", buf.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := pids(t, pidfile); len(got) != 2 {
		t.Fatalf("started %v", got)
	}

	// Ctrl-C reaches the foreground process group of a terminal: capture and
	// the command it started, but not the services, which have groups of their own.
	syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
	select {
	case err := <-exited:
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 2 {
			t.Errorf("capture ended with %v, want exit status 2\n%s", err, buf.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("capture did not stop after SIGINT:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "interrupted") {
		t.Errorf("output %q does not say that the run was interrupted", buf.String())
	}
	noneLeft(t, pidfile, 2)
}

func TestServicesDieWithTheirCapture(t *testing.T) {
	// Even when capture is killed and cannot clean up, the services notice that
	// their standard input is closed.
	dir := standInDir(t)
	pidfile := filepath.Join(t.TempDir(), "pids")
	started := filepath.Join(t.TempDir(), "started")
	shpid := filepath.Join(t.TempDir(), "shpid")
	page := servicePage(t, "sigkill.md", "services: [fakes3]\n", fmt.Sprintf("```sh run\necho $$ > %s\ntouch %s\nsleep 60\n```\n", shpid, started))
	cmd := exec.Command(filepath.Join(dir, "capture"), "-bin", fakeBin(t), "-services", dir, page)
	cmd.Env = append(os.Environ(), "STANDIN_PIDFILE="+pidfile, "TMPDIR="+t.TempDir())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	defer syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	defer func() {
		// The command of the page has a process group of its own.
		if data, err := os.ReadFile(shpid); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				syscall.Kill(-n, syscall.SIGKILL)
			}
		}
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the page did not start its command")
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := pids(t, pidfile)
	if len(got) != 1 {
		t.Fatalf("started %v", got)
	}
	pid, _ := strconv.Atoi(got[0][1])
	// Only capture dies: the page's command (sleep 60) keeps the group alive
	// and is killed with it by the deferred call.
	syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)
	<-exited
	deadline = time.Now().Add(5 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(pid) {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("the service %d outlived the capture that started it", pid)
	}
}

// TestRealFakes3 builds tools/fakes3, starts it on a free port and on a named
// one for a page and uses the bucket through the environment variables.
func TestRealFakes3(t *testing.T) {
	if testing.Short() {
		t.Skip("builds tools/fakes3")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on the path")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is not on the path")
	}
	port := freePort(t)
	page := servicePage(t, "real.md", fmt.Sprintf("services:\n  - fakes3:%d\n", port), fmt.Sprintf(""+
		"```text file=url.txt\n{{FAKES3_URL}}/bucket/key\n```\n\n"+
		"```sh run\ncurl -s -X PUT -d 'first value' \"$(cat url.txt)\"\ncurl -s \"$FAKES3_URL/bucket/key\"; echo\ncurl -s -o /dev/null -w '%%{http_code}\\n' \"$FAKES3_URL/bucket/missing\"\ncurl -s \"$FAKES3_URL/bucket?list-type=2&prefix=k\" | grep -o '<Key>[^<]*</Key>'\n```\n\n"+
		"```text out\nfirst value\n404\n<Key>key</Key>\n```\n\n"+
		"```sh run\ncurl -s -o /dev/null -w '%%{http_code}\\n' http://127.0.0.1:%d/bucket/key\n```\n\n```text out\n200\n```\n", port))
	start := time.Now()
	code, out, errb := capture(t, "-bin", fakeBin(t), page)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	t.Logf("built and ran in %s", time.Since(start).Round(time.Millisecond))
	// The bucket is gone with the page: nothing listens on the port now.
	if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond); err == nil {
		c.Close()
		t.Error("fakes3 is still running after the page")
	}
	// A second page starts with an empty bucket.
	second := servicePage(t, "second.md", fmt.Sprintf("services:\n  - fakes3:%d\n", port),
		"```sh run\ncurl -s -o /dev/null -w '%{http_code}\\n' \"$FAKES3_URL/bucket/key\"\n```\n\n```text out\n404\n```\n")
	if code, out, errb := capture(t, "-bin", fakeBin(t), second); code != 0 {
		t.Errorf("a second page: exit %d\n%s%s", code, out, errb)
	}
}

func TestBuildServicesNamesTheMissingPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go build")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on the path")
	}
	if _, _, err := buildServices(context.Background(), t.TempDir(), []string{"fakes3"}); err == nil || !strings.Contains(err.Error(), "building fakes3") {
		t.Errorf("err = %v", err)
	}
}

func TestPortGateRespectsTheContext(t *testing.T) {
	var g portGate
	port := freePort(t)
	release, err := g.acquire(context.Background(), []int{port})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := g.acquire(ctx, []int{port}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a second acquire of a held port: %v, want the context's error", err)
	}
	release()
	rel2, err := g.acquire(context.Background(), []int{port})
	if err != nil {
		t.Fatal(err)
	}
	rel2()
}

func TestPortGateBetweenProcesses(t *testing.T) {
	// A second gate stands for a second capture process: it shares nothing but
	// the lock file.
	var a, b portGate
	port := freePort(t)
	release, err := a.acquire(context.Background(), []int{port})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := b.acquire(ctx, []int{port}); err == nil {
		t.Skip("lock files are not supported here")
	}
	release()
	rel2, err := b.acquire(context.Background(), []int{port})
	if err != nil {
		t.Fatal(err)
	}
	rel2()
}

func TestServiceOutputTail(t *testing.T) {
	if got := tail("a\n\n b \nc\nd\n", 2); got != "c\nd" {
		t.Errorf("tail = %q", got)
	}
}

func TestUpdateHidesThePortThatWasChosen(t *testing.T) {
	dir := standInDir(t)
	page := servicePage(t, "update.md", "services: [fakes3]\n", "```sh run\necho \"$FAKES3_URL\"\n```\n")
	code, out, errb := run1(t, dir, "-update", page)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	got, _ := os.ReadFile(page)
	if !strings.Contains(string(got), "```text out\nhttp://127.0.0.1:<fakes3-port>\n```") {
		t.Errorf("the out block holds the port that was chosen:\n%s", got)
	}
	if code, out, errb := run1(t, dir, "-check", page); code != 0 {
		t.Errorf("-check after -update: exit %d\n%s%s", code, out, errb)
	}
}

func TestInitRunsOneAtATimeAcrossProcesses(t *testing.T) {
	// Two capture runs in one process stand for two processes: each has its own
	// runner and so its own mutex, and they share the lock file of the home.
	t.Setenv("CAPTURE_TEST_LOCK", filepath.Join(t.TempDir(), "lock"))
	t.Setenv("PRAVO_HOME", filepath.Join(t.TempDir(), "home"))
	page := func() string {
		return writePage(t, "init.md", head+"---\n\n```sh run\npravo init\n```\n")
	}
	codes := make(chan int, 4)
	for i := 0; i < 4; i++ {
		p := page()
		go func() {
			code, _, _ := capture(t, "-bin", fakeBin(t), p)
			codes <- code
		}()
	}
	for i := 0; i < 4; i++ {
		if code := <-codes; code != 0 {
			t.Errorf("exit %d: two runs ran pravo init at the same time", code)
		}
	}
}

func TestAPageCanStopItsService(t *testing.T) {
	dir := standInDir(t)
	pidfile := filepath.Join(t.TempDir(), "pids")
	t.Setenv("STANDIN_PIDFILE", pidfile)
	page := servicePage(t, "kill.md", "services: [fakes3, fakeoci]\n", ""+
		"```sh run\nif httpget \"$FAKES3_URL\" >/dev/null; then echo up; fi\nkill \"$FAKES3_PID\"\nsleep 0.3\nif httpget \"$FAKES3_URL\" 2>/dev/null; then echo up; else echo down; fi\nhttpget \"$FAKEOCI_URL\"\n```\n\n"+
		"```text out\nup\ndown\nfakeoci stand-in\n```\n")
	if code, out, errb := run1(t, dir, page); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	noneLeft(t, pidfile, 2)
}

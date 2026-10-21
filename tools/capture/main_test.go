package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var updateGolden = flag.Bool("golden", false, "rewrite the golden files in testdata/golden")

// fakeBin returns the stand-in pravo of the tests.
func fakeBin(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("testdata/fakepravo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// capture runs the tool in process and returns its exit status and output.
func capture(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// golden compares got with testdata/golden/name, or rewrites the file when
// the tests run with -golden.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the golden file; run the tests with -golden to see the change in a diff.\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// copyPage copies a test page into a temporary directory.
func copyPage(t *testing.T, name string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", "pages", name))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

func writePage(t *testing.T, name, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPassingPage(t *testing.T) {
	code, out, errb := capture(t, "-check", "-bin", fakeBin(t), "testdata/pages/ok.md")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	if want := "ok      testdata/pages/ok.md  14 commands\ncapture: 1 page, 14 commands run, no differences\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if errb != "" {
		t.Errorf("stderr = %q, want nothing", errb)
	}
}

func TestRevealIsVerifiedInACopy(t *testing.T) {
	head := "---\ntitle: T\ndescription: D\norder: 1\n---\n\n```cue file=main.cue\npackage main\n```\n\n:::predict What is printed?\n\n::reveal\n\n"
	tail := "```sh run\nrm main.cue\n```\n\n:::\n\n```sh run\nls\n```\n\n```text out\nmain.cue\n```\n"
	good := writePage(t, "good.md", head+"```sh run\necho answer\n```\n\n```text out\nanswer\n```\n\n"+tail)
	code, out, errb := capture(t, "-check", "-bin", fakeBin(t), good)
	if code != 0 || !strings.Contains(out, "3 commands") {
		t.Errorf("a true answer in a ::reveal, and a command that deletes a file in it: exit %d\n%s%s", code, out, errb)
	}
	bad := writePage(t, "bad.md", head+"```sh run\necho answer\n```\n\n```text out\nwrong\n```\n\n"+tail)
	code, out, _ = capture(t, "-check", "-bin", fakeBin(t), bad)
	if code != 1 || !strings.Contains(out, "block 2 (reveal)") || !strings.Contains(out, "actual   (output line 1): answer") {
		t.Errorf("a false answer in a ::reveal must fail: exit %d\n%s", code, out)
	}
}

func TestCheckIsTheDefault(t *testing.T) {
	code, _, _ := capture(t, "-bin", fakeBin(t), "testdata/pages/fail.md")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

func TestFailingPageReport(t *testing.T) {
	code, out, _ := capture(t, "-check", "-bin", fakeBin(t), "testdata/pages/fail.md")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	golden(t, "fail.txt", out)
}

func TestAuthoringErrorsAreReportedWithoutRunning(t *testing.T) {
	code, out, _ := capture(t, "-check", "-bin", fakeBin(t), "testdata/pages/bad.md")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	golden(t, "bad.txt", out)
}

func TestWarningsDoNotFail(t *testing.T) {
	code, out, _ := capture(t, "-check", "-bin", fakeBin(t), "testdata/pages/warn.md")
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	golden(t, "warn.txt", out)
}

func TestSkippedPages(t *testing.T) {
	for _, page := range []string{"nocapture.md", "plain.md"} {
		code, out, errb := capture(t, "-bin", fakeBin(t), "testdata/pages/"+page)
		if code != 0 || out != "" || errb != "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want a silent success", page, code, out, errb)
		}
	}
	_, out, _ := capture(t, "-v", "-bin", fakeBin(t), "testdata/pages/nocapture.md")
	if want := "skip    testdata/pages/nocapture.md  (capture: false)\n"; out != want {
		t.Errorf("-v stdout = %q, want %q", out, want)
	}
}

func TestSkippedPagesNeedNoBinary(t *testing.T) {
	code, out, errb := capture(t, "-bin", "/nonexistent/pravo", "testdata/pages/plain.md")
	if code != 0 {
		t.Errorf("exit %d, want 0\n%s%s", code, out, errb)
	}
}

func TestUpdateRewritesOnlyOutBlocks(t *testing.T) {
	page := copyPage(t, "stale.md")
	code, out, errb := capture(t, "-update", "-bin", fakeBin(t), page)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	if !strings.Contains(out, "3 out blocks rewritten, 4 added, 1 removed") {
		t.Errorf("summary missing from %q", out)
	}
	got, _ := os.ReadFile(page)
	golden(t, "stale.md", string(got))

	// The rewritten page passes, and a second update changes nothing.
	if code, out, _ := capture(t, "-check", "-bin", fakeBin(t), page); code != 0 {
		t.Errorf("-check after -update: exit %d\n%s", code, out)
	}
	if code, out, _ := capture(t, "-update", "-bin", fakeBin(t), page); code != 0 || strings.Contains(out, "updated") {
		t.Errorf("second -update: exit %d, output %q; want no change", code, out)
	}
	again, _ := os.ReadFile(page)
	if !bytes.Equal(got, again) {
		t.Errorf("the second -update changed the page")
	}
}

func TestUpdateKeepsCommandsAndFiles(t *testing.T) {
	page := copyPage(t, "stale.md")
	before, _ := os.ReadFile(page)
	capture(t, "-update", "-bin", fakeBin(t), page)
	after, _ := os.ReadFile(page)
	if a, b := notOut(before), notOut(after); a != b {
		t.Errorf("-update changed a block that is not an out block:\n--- before\n%s\n--- after\n%s", a, b)
	}
}

// notOut returns the info strings and contents of every block that is not an
// out block, which -update must leave as they are.
func notOut(src []byte) string {
	var keep []string
	for _, b := range parsePage("x", src).Blocks {
		if !b.Out {
			keep = append(keep, b.Info+"\n"+strings.Join(b.Content, "\n"))
		}
	}
	return strings.Join(keep, "\n--\n")
}

func TestUpdateReportsAnUnexpectedExit(t *testing.T) {
	page := writePage(t, "exit.md", "---\ntitle: T\ndescription: D\norder: 1\n---\n\n```sh run\npravo fail\n```\n\n```text out\nold\n```\n")
	code, out, _ := capture(t, "-update", "-bin", fakeBin(t), page)
	if code != 1 || !strings.Contains(out, "exit status 3, expected 0") {
		t.Errorf("exit %d, output %q; want 1 and the exit status", code, out)
	}
	got, _ := os.ReadFile(page)
	if !strings.Contains(string(got), "```text out\nboom: .\n```") {
		t.Errorf("the out block was not rewritten:\n%s", got)
	}
	if strings.Contains(string(got), "exit=") {
		t.Errorf("-update changed the command line:\n%s", got)
	}
}

func TestUpdateRefusesCRLF(t *testing.T) {
	page := writePage(t, "crlf.md", "---\r\ntitle: T\r\ndescription: D\r\norder: 1\r\n---\r\n\r\n```sh run\r\necho hi\r\n```\r\n")
	before, _ := os.ReadFile(page)
	code, out, _ := capture(t, "-update", "-bin", fakeBin(t), page)
	after, _ := os.ReadFile(page)
	if code != 1 || !strings.Contains(out, "CRLF") || !bytes.Equal(before, after) {
		t.Errorf("exit %d, output %q; want a refusal that leaves the file alone", code, out)
	}
	if code, out, _ := capture(t, "-check", "-bin", fakeBin(t), page); code != 1 || !strings.Contains(out, "no out block") {
		t.Errorf("-check of a CRLF page: exit %d, output %q", code, out)
	}
}

func TestWalkSkipsHiddenDirectoriesAndCaptureFalse(t *testing.T) {
	code, out, errb := capture(t, "-v", "-bin", fakeBin(t), "testdata/tree")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	want := "ok      testdata/tree/a.md  1 command\n" +
		"ok      testdata/tree/sub/b.md  1 command\n" +
		"skip    testdata/tree/sub/skip.md  (capture: false)\n" +
		"capture: 2 pages, 2 commands run, no differences\n"
	if out != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", out, want)
	}
}

func TestParallelRunsReportInPageOrder(t *testing.T) {
	_, seq, _ := capture(t, "-j", "1", "-bin", fakeBin(t), "testdata/tree", "testdata/pages/ok.md", "testdata/pages/fail.md")
	_, par, _ := capture(t, "-j", "0", "-bin", fakeBin(t), "testdata/tree", "testdata/pages/ok.md", "testdata/pages/fail.md")
	if seq != par {
		t.Errorf("-j 0 report differs from -j 1\n--- parallel\n%s\n--- sequential\n%s", par, seq)
	}
	if !strings.Contains(seq, "FAIL    testdata/pages/fail.md") {
		t.Errorf("the failing page is missing from the report:\n%s", seq)
	}
}

func TestEveryPageStartsEmpty(t *testing.T) {
	// a.md writes a.txt; sub/b.md lists its workspace and expects nothing.
	for _, j := range []string{"1", "2"} {
		if code, out, _ := capture(t, "-j", j, "-bin", fakeBin(t), "testdata/tree/a.md", "testdata/tree/sub/b.md"); code != 0 {
			t.Errorf("-j %s: exit %d\n%s", j, code, out)
		}
	}
}

func TestJSONForOnePage(t *testing.T) {
	file := filepath.Join(t.TempDir(), "page.json")
	code, out, _ := capture(t, "-json", file, "-bin", fakeBin(t), "testdata/pages/ok.md")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	var got jsonPage
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	if got.Page != "testdata/pages/ok.md" || got.Title != "A passing page" {
		t.Errorf("page = %q, title = %q", got.Page, got.Title)
	}
	if strings.Join(got.Version, "|") != "pravo 0.0.0-test|cue v0.0.0" {
		t.Errorf("version = %q", got.Version)
	}
	if len(got.Blocks) != 14 {
		t.Fatalf("%d blocks, want 14", len(got.Blocks))
	}
	first := got.Blocks[0]
	if first.Command != "pravo eval\nls" || first.Exit != 0 || first.Stage != "main" || !first.OK ||
		first.Output != "evaluating .\nreal .\nfile ./main.cue\nmain.cue" {
		t.Errorf("first block = %+v", first)
	}
	if b := got.Blocks[1]; !b.Hidden || b.Output != "noise" {
		t.Errorf("hidden block = %+v", b)
	}
	if b := got.Blocks[2]; b.Exit != 3 || b.ExpectExit != 3 || b.Output != "boom: ." {
		t.Errorf("exit block = %+v", b)
	}
	var stages []string
	for _, b := range got.Blocks {
		stages = append(stages, b.Stage)
	}
	if s := strings.Join(stages, ","); !strings.Contains(s, "solution,solution,main") || !strings.Contains(s, "deep-dive,main") {
		t.Errorf("stages = %s", s)
	}
}

func TestJSONForSeveralPages(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	if code, out, _ := capture(t, "-json", dir, "-bin", fakeBin(t), "testdata/tree"); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, name := range []string{"a.json", "sub/b.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "skip.json")); err == nil {
		t.Errorf("a skipped page has a JSON file")
	}
}

func TestJSONIsWrittenForAFailingPage(t *testing.T) {
	file := filepath.Join(t.TempDir(), "fail.json")
	code, _, _ := capture(t, "-json", file, "-bin", fakeBin(t), "testdata/pages/fail.md")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	var got jsonPage
	data, _ := os.ReadFile(file)
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	failed := 0
	for _, b := range got.Blocks {
		if !b.OK {
			failed++
		}
	}
	if failed != 8 || len(got.Blocks) != 9 {
		t.Errorf("%d of %d blocks marked failed, want 8 of 9", failed, len(got.Blocks))
	}
}

func TestTimeout(t *testing.T) {
	page := writePage(t, "slow.md", "---\ntitle: T\ndescription: D\norder: 1\n---\n\n```sh run\npravo slow\n```\n")
	start := time.Now()
	code, out, _ := capture(t, "-timeout", "300ms", "-bin", fakeBin(t), page)
	if code != 1 || !strings.Contains(out, "did not finish within 300ms") {
		t.Errorf("exit %d, output %q", code, out)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %s: the command was not killed", d)
	}
}

func TestUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"both modes":      {"-check", "-update", "testdata/pages/ok.md"},
		"missing page":    {"-bin", "testdata/fakepravo", "testdata/pages/missing.md"},
		"missing -bin":    {"-bin", "testdata/nothing", "testdata/pages/ok.md"},
		"bin not a file":  {"-bin", "testdata", "testdata/pages/ok.md"},
		"unknown flag":    {"-nope"},
		"directory empty": {t.TempDir()},
	}
	for name, args := range cases {
		if code, _, _ := capture(t, args...); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
}

func TestCommandsSeeTheCallersEnvironment(t *testing.T) {
	// PRAVO_HOME reaches the commands; capture shows its value as ~/.pravo.
	t.Setenv("PRAVO_HOME", "/somewhere/else")
	t.Setenv("CAPTURE_TEST_VALUE", "kept")
	t.Setenv("PRAVO_LOG", "debug")
	page := writePage(t, "env.md", "---\ntitle: T\ndescription: D\norder: 1\n---\n\n"+
		"```sh run\necho \"$PRAVO_HOME $CAPTURE_TEST_VALUE\"\npravo env\n```\n\n"+
		"```text out\n~/.pravo kept\nPRAVO_LOG=unset NO_COLOR=1\n```\n")
	if code, out, _ := capture(t, "-bin", fakeBin(t), page); code != 0 {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestKeepPrintsTheWorkspace(t *testing.T) {
	page := writePage(t, "keep.md", "---\ntitle: T\ndescription: D\norder: 1\n---\n\n```text file=kept.txt\nhello\n```\n\n```sh run hidden\ntrue\n```\n")
	code, out, _ := capture(t, "-keep", "-bin", fakeBin(t), page)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	const marker = "workspace kept in "
	i := strings.Index(out, marker)
	if i < 0 {
		t.Fatalf("no workspace in %q", out)
	}
	ws := strings.TrimSpace(strings.SplitN(out[i+len(marker):], "\n", 2)[0])
	defer os.RemoveAll(filepath.Dir(ws))
	if data, err := os.ReadFile(filepath.Join(ws, "kept.txt")); err != nil || string(data) != "hello\n" {
		t.Errorf("kept.txt = %q, %v", data, err)
	}
}

// TestRealPravo runs a page against the real binary, built from the module.
// It checks the properties that do not depend on pravo's wording: the page
// is rewritten, then passes, and no path of the machine reaches it.
func TestRealPravo(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pravo binary")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on the path")
	}
	page := writePage(t, "real.md", "---\ntitle: Real\ndescription: D\norder: 1\n---\n\n"+
		"```cue file=main.cue\npackage main\n\nsize: int & >=1 & <=10\nsize: 3\n```\n\n"+
		"```sh run hidden\npravo init\n```\n\n"+
		"```cue file=main.cue\npackage main\n\nsize: int & >=1 & <=10\nsize: 3\nsize: 11\n```\n\n"+
		"```sh run exit=6\npravo eval\n```\n")
	code, out, errb := capture(t, "-update", page)
	if code != 0 {
		t.Fatalf("-update: exit %d\n%s%s", code, out, errb)
	}
	got, _ := os.ReadFile(page)
	text := string(got)
	if !strings.Contains(text, "```text out\n") || !strings.Contains(text, "conflicting values") || !strings.Contains(text, "./main.cue:") {
		t.Errorf("the conflict is not in the rewritten page:\n%s", text)
	}
	if strings.Contains(text, filepath.Dir(page)) || strings.Contains(text, filepath.Join(os.TempDir(), "capture-")) {
		t.Errorf("a temporary path reached the page:\n%s", text)
	}
	if code, out, errb := capture(t, "-check", page); code != 0 {
		t.Errorf("-check: exit %d\n%s%s", code, out, errb)
	}
}

func TestShellSemantics(t *testing.T) {
	// A visible block runs every line, as the reader's shell would, and its
	// status is that of the last command. Hidden and check blocks stop at the
	// first command that fails.
	ok := writePage(t, "visible.md", "---\ntitle: T\ndescription: D\norder: 1\n---\n\n"+
		"```sh run\nfalse\necho \"after: $?\"\n```\n\n```text out\nafter: 1\n```\n")
	if code, out, _ := capture(t, "-bin", fakeBin(t), ok); code != 0 {
		t.Errorf("visible block: exit %d\n%s", code, out)
	}
	for _, kind := range []string{"hidden run", "check"} {
		page := writePage(t, "strict.md", "---\ntitle: T\ndescription: D\norder: 1\n---\n\n"+
			"```sh "+kind+"\nfalse\ntrue\n```\n")
		code, out, _ := capture(t, "-bin", fakeBin(t), page)
		if code != 1 || !strings.Contains(out, "exit status 1, expected 0") {
			t.Errorf("%s block: exit %d, output %q; want a failure", kind, code, out)
		}
	}
}

func TestSummaryWhenNothingRuns(t *testing.T) {
	_, out, _ := capture(t, "-bin", fakeBin(t), "testdata/pages/plain.md", "testdata/pages/nocapture.md")
	if want := "capture: 2 pages read, none with run blocks\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestInitRunsOneAtATime(t *testing.T) {
	t.Setenv("CAPTURE_TEST_LOCK", filepath.Join(t.TempDir(), "lock"))
	var pages []string
	args := []string{"-j", "4", "-bin", fakeBin(t)}
	for i := 0; i < 4; i++ {
		pages = append(pages, writePage(t, "init.md", "---\ntitle: T\ndescription: D\norder: 1\n---\n\n```sh run\npravo init\n```\n"))
	}
	code, out, _ := capture(t, append(args, pages...)...)
	if code != 0 {
		t.Errorf("exit %d: two pages ran pravo init at the same time\n%s", code, out)
	}
}

func TestPravoHomeOfTheCallerIsShownAsTheDefault(t *testing.T) {
	// pravo prints the provider store under its home. A page shows ~/.pravo,
	// and must pass whether or not the caller moved the home.
	t.Setenv("PRAVO_HOME", filepath.Join(t.TempDir(), "elsewhere", "home"))
	page := writePage(t, "home.md", "---\ntitle: T\ndescription: D\norder: 1\n---\n\n"+
		"```sh run\necho \"$PRAVO_HOME/providers/registry.opentofu.org\"\n```\n\n"+
		"```text out\n~/.pravo/providers/registry.opentofu.org\n```\n")
	if code, out, _ := capture(t, "-bin", fakeBin(t), page); code != 0 {
		t.Errorf("exit %d\n%s", code, out)
	}
}

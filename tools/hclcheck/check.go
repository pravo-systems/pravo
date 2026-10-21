package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Status values of the table.
const (
	statusOK       = "ok"             // valid as written
	statusStubs    = "ok+stubs"       // valid once the root was completed
	statusExpected = "expected error" // meant to fail, and does
	statusSkipped  = "skipped"
	statusFail     = "FAIL"
)

// A Result is the outcome of one block.
type Result struct {
	Block  *Block
	Status string
	Stage  string // assemble, fmt, init, validate or console, when the status is FAIL or expected
	Note   string // one line
	Detail string // the output of the failing command
}

// A runner starts tofu.
type runner struct {
	tofu    string
	env     []string
	timeout time.Duration
	initMu  sync.Mutex // init writes the shared plugin cache
}

func newRunner(tofu, cache string, timeout time.Duration) *runner {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "TF_") || strings.HasPrefix(k, "AWS_") || k == "CHECKPOINT_DISABLE" {
			continue
		}
		env = append(env, kv)
	}
	// The aws provider asks for a region while it validates a configuration.
	env = append(env, "AWS_REGION=eu-west-1", "TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1", "TF_PLUGIN_CACHE_DIR="+cache)
	return &runner{tofu: tofu, env: env, timeout: timeout}
}

// run starts tofu in dir with stdin and returns the merged output and the exit
// status. A status of -1 means tofu did not start or ran too long.
func (r *runner) run(dir, stdin string, args ...string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.tofu, args...)
	cmd.Dir = dir
	cmd.Env = r.env
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err == nil {
		return out.String(), 0
	}
	if ee, ok := err.(*exec.ExitError); ok && ctx.Err() == nil {
		return out.String(), ee.ExitCode()
	}
	return out.String() + err.Error(), -1
}

// A diagnostic is one entry of `tofu validate -json`.
type diagnostic struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail"`
	Range    *struct {
		Filename string `json:"filename"`
		Start    struct {
			Line int `json:"line"`
		} `json:"start"`
	} `json:"range"`
}

type validation struct {
	Valid       bool         `json:"valid"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

func (d diagnostic) String() string {
	loc := ""
	if d.Range != nil {
		loc = fmt.Sprintf("%s:%d: ", d.Range.Filename, d.Range.Start.Line)
	}
	s := loc + d.Summary
	if d.Detail != "" {
		s += ": " + strings.Join(strings.Fields(d.Detail), " ")
	}
	return s
}

// text lists the errors and warnings, one per line.
func (v validation) text() string {
	var lines []string
	for _, d := range v.Diagnostics {
		lines = append(lines, d.Severity+": "+d.String())
	}
	return strings.Join(lines, "\n")
}

// checkPage assembles the blocks of one page into a workspace, in order, and
// checks each block after it is written. A file that a block names again is
// replaced, unless the title says "lines A-B": then the block is appended.
// The root of a block is the directory of its file; the files that earlier
// blocks wrote there are part of it.
func (r *runner) checkPage(base string, blocks []Block) []Result {
	ws := filepath.Join(base, slug(blocks[0].Page))
	if err := os.MkdirAll(ws, 0o755); err != nil {
		return failAll(blocks, "assemble", err.Error())
	}
	written := map[string]bool{}
	var results []Result
	for i := range blocks {
		b := &blocks[i]
		res := Result{Block: b}
		switch {
		case b.Skip != "":
			res.Status, res.Note = statusSkipped, b.Skip
		case len(b.Files) > 1:
			res.Status, res.Stage = statusFail, "assemble"
			res.Note = "the block shows " + fmt.Sprint(len(b.Files)) + " files (" + strings.Join(b.Files, ", ") + "): one block, one file"
		default:
			r.checkBlock(ws, b, written, &res)
		}
		results = append(results, res)
	}
	return results
}

func failAll(blocks []Block, stage, msg string) []Result {
	var out []Result
	for i := range blocks {
		out = append(out, Result{Block: &blocks[i], Status: statusFail, Stage: stage, Note: msg})
	}
	return out
}

func (r *runner) checkBlock(ws string, b *Block, written map[string]bool, res *Result) {
	fail := func(stage, note, detail string) {
		res.Stage, res.Note, res.Detail = stage, note, detail
		res.Status = statusFail
		own := stage != "assemble" && !strings.HasPrefix(note, "carried over") && !strings.HasPrefix(note, "in completion")
		if b.Expect == "error" && own && (b.Match == "" || strings.Contains(note+detail, b.Match)) {
			res.Status = statusExpected
		}
	}

	file := filepath.Join(ws, filepath.FromSlash(b.Path))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		fail("assemble", err.Error(), "")
		return
	}
	body := b.Body
	prev, readErr := os.ReadFile(file)
	if b.Excerpt && written[b.Path] {
		body = string(prev) + "\n" + body
	}
	written[b.Path] = true
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		fail("assemble", err.Error(), "")
		return
	}
	// A block that is meant to fail leaves no trace for the blocks after it.
	defer func() {
		if b.Expect != "error" {
			return
		}
		if readErr == nil {
			os.WriteFile(file, prev, 0o644)
		} else {
			os.Remove(file)
		}
	}()
	dir := filepath.Dir(file)
	rel, _ := filepath.Rel(ws, dir)

	add, err := complete(ws, rel)
	if err != nil {
		fail("assemble", err.Error(), "")
		return
	}
	if s := add.String(); s != "" {
		res.Note = "added " + s
	}

	if out, code := r.run(dir, "", "fmt", "-check", "-diff", "-no-color", filepath.Base(file)); code != 0 {
		if code == 3 {
			fail("fmt", "tofu fmt would change the block", out)
		} else {
			fail("fmt", firstLine(out), out)
		}
		return
	}
	if tf, _ := filepath.Glob(filepath.Join(dir, "*.tf")); len(tf) == 0 {
		// A root that holds only variable files: nothing to initialise.
		res.Status = statusOK
		res.Note = strings.TrimSpace("fmt only. " + res.Note)
		return
	}

	r.initMu.Lock()
	out, code := r.run(dir, "", "init", "-backend=false", "-input=false", "-no-color")
	r.initMu.Unlock()
	if code != 0 {
		fail("init", firstError(out), out)
		return
	}

	out, code = r.run(dir, "", "validate", "-json", "-no-color")
	var v validation
	if jerr := json.Unmarshal([]byte(out), &v); jerr != nil {
		fail("validate", firstLine(out), out)
		return
	}
	var own, carried *diagnostic
	for i, d := range v.Diagnostics {
		if d.Severity != "error" {
			continue
		}
		if d.Range != nil && d.Range.Filename != filepath.Base(file) {
			if carried == nil {
				carried = &v.Diagnostics[i]
			}
		} else if own == nil {
			own = &v.Diagnostics[i]
		}
	}
	switch {
	case own != nil:
		fail("validate", own.String(), v.text())
		return
	case carried != nil && carried.Range.Filename == stubFile:
		fail("validate", "in completion: "+carried.String(), v.text())
		return
	case carried != nil:
		// An earlier block of the page left the root invalid.
		fail("validate", "carried over: "+carried.String(), v.text())
		return
	case code != 0:
		fail("validate", firstLine(out), out)
		return
	}

	if hasVarsFile(dir) {
		out, code := r.run(dir, "0\n", "console", "-no-color")
		if code != 0 || strings.Contains(out, "Warning") {
			fail("console", firstError(out), out)
			return
		}
	}

	if b.Expect == "error" {
		res.Status, res.Stage = statusFail, "validate"
		res.Note = "expected an error and found none"
		return
	}
	res.Status = statusOK
	if len(add.stubs) > 0 {
		res.Status = statusStubs
	}
}

func hasVarsFile(dir string) bool {
	for _, pat := range []string{"*.tfvars", "*.tfvars.json"} {
		if m, _ := filepath.Glob(filepath.Join(dir, pat)); len(m) > 0 {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// firstError returns the first "Error:" or "Warning:" line of tofu output with
// the line that follows it.
func firstError(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l = strings.TrimLeft(l, "│╷ ")
		if strings.HasPrefix(l, "Error:") || strings.HasPrefix(l, "Warning:") {
			msg := l
			for _, n := range lines[i+1:] {
				n = strings.Trim(strings.TrimLeft(n, "│╵╷ "), " ")
				if n != "" {
					msg += ": " + n
					break
				}
			}
			return msg
		}
	}
	return firstLine(s)
}

var slugRe = regexp.MustCompile(`[^A-Za-z0-9]+`)

func slug(page string) string { return strings.Trim(slugRe.ReplaceAllString(page, "-"), "-") }

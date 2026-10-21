// Command capture runs the examples in the documentation and compares what
// they print with what the pages say.
//
// It reads the Markdown pages named on the command line, or every page below
// a directory, and for each page creates an empty workspace, writes the
// file= blocks, runs the run blocks with pravo on the path and compares the
// output with the out block that follows each of them. A page that needs a
// server lists it in its front matter (services: [fakes3:9000, fakeoci]) and
// capture starts it for the page and stops it after. See README.md.
//
//	go run ./tools/capture -check docs/learn/a03-writing-a-field-twice.md
//	go run ./tools/capture -check ./docs
//	go run ./tools/capture -update docs/learn/a03-writing-a-field-twice.md
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage: capture [-check | -update] [-bin PATH] [-services DIR] [-j N] [-json OUT] [-timeout D] [-keep] [-v] [PATH...]

capture runs the run blocks of documentation pages and compares their output
with the out blocks. PATH is a Markdown file or a directory that is searched
for pages; the default is the docs directory of the module.

`

// run is main with its streams and arguments passed in. It returns the exit
// status: 0 when every page agrees, 1 when a page does not, 2 when capture
// cannot do its work.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("capture", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, usage)
		flags.PrintDefaults()
	}
	check := flags.Bool("check", false, "compare output with the out blocks and fail on any difference (default)")
	update := flags.Bool("update", false, "rewrite the out blocks from a real run; commands and files are never changed")
	bin := flags.String("bin", "", "pravo binary to run (default: build ./cmd/pravo once)")
	servicesDir := flags.String("services", "", "directory with the executables of the services the pages list (default: build them from the module)")
	jobs := flags.Int("j", 1, "pages to run in parallel; 0 means one per CPU")
	jsonOut := flags.String("json", "", "write the run blocks of a page to this file, or of several pages to this directory")
	timeout := flags.Duration("timeout", 5*time.Minute, "longest a single run block may take")
	keep := flags.Bool("keep", false, "keep the workspaces and print where they are")
	verbose := flags.Bool("v", false, "print every block as it runs, and the pages that are skipped")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *check && *update {
		fmt.Fprintln(stderr, "capture: -check and -update are exclusive")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "capture: -timeout must be positive")
		return 2
	}

	paths := flags.Args()
	if len(paths) == 0 {
		paths = []string{defaultDocs()}
	}
	files, roots, err := collectPages(paths)
	if err != nil {
		fmt.Fprintf(stderr, "capture: %v\n", err)
		return 2
	}
	if len(files) == 0 {
		fmt.Fprintln(stderr, "capture: no Markdown pages found")
		return 2
	}
	var pages []*page
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(stderr, "capture: %v\n", err)
			return 2
		}
		pages = append(pages, parsePage(f, src))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	needBin := false
	for _, p := range pages {
		if p.FM.Capture && len(p.Errors) == 0 && p.runnable() {
			needBin = true
		}
	}
	binDir, version := "", []string(nil)
	if needBin {
		var cleanup func()
		binDir, version, cleanup, err = prepareBinary(ctx, *bin, paths, *verbose, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "capture: %v\n", err)
			return 2
		}
		defer cleanup()
	}

	if *verbose && len(version) > 0 {
		fmt.Fprintf(stderr, "capture: %s\n", strings.Join(version, ", "))
	}
	r := newRunner(config{Update: *update, Verbose: *verbose, Keep: *keep, Timeout: *timeout, Log: &syncWriter{w: stderr}}, binDir)
	if names := neededServices(pages); len(names) > 0 {
		var cleanup func()
		r.services, cleanup, err = prepareServices(ctx, *servicesDir, names, paths, *verbose, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "capture: %v\n", err)
			return 2
		}
		defer cleanup()
	}
	var t totals
	results := runPages(ctx, r, pages, *jobs, func(res *pageResult) {
		printResult(stdout, res, *update, *verbose, &t)
	})

	if *jsonOut != "" {
		if err := writeJSON(*jsonOut, results, roots, version); err != nil {
			fmt.Fprintf(stderr, "capture: %v\n", err)
			return 2
		}
	}
	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "capture: interrupted")
		return 2
	}
	switch {
	case t.Ran > 0 || t.Failed > 0:
		fmt.Fprintln(stdout, t.line(*update))
	case t.Pages > 1:
		fmt.Fprintf(stdout, "capture: %s read, none with run blocks\n", plural(t.Pages, "page"))
	}
	if t.Failed > 0 {
		return 1
	}
	return 0
}

// runPages runs the pages with at most jobs at a time. report is called for
// each result in page order, as soon as that page and all before it are done.
func runPages(ctx context.Context, r *runner, pages []*page, jobs int, report func(*pageResult)) []*pageResult {
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	jobs = max(1, min(jobs, len(pages)))
	done := make([]chan *pageResult, len(pages))
	for i := range done {
		done[i] = make(chan *pageResult, 1)
	}
	work := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < jobs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				done[i] <- r.runPage(ctx, pages[i])
			}
		}()
	}
	go func() {
		for i := range pages {
			work <- i
		}
		close(work)
	}()
	results := make([]*pageResult, 0, len(pages))
	for i := range pages {
		res := <-done[i]
		results = append(results, res)
		report(res)
	}
	wg.Wait()
	return results
}

// defaultDocs is the docs directory of the module that contains the current
// directory, or ./docs.
func defaultDocs() string {
	if root, err := findModuleRoot("."); err == nil {
		if rel, err := filepath.Rel(mustGetwd(), filepath.Join(root, "docs")); err == nil {
			return rel
		}
	}
	return "docs"
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}

// collectPages expands the arguments to Markdown files: a file stands for
// itself, a directory for every .md file below it, in path order. Directories
// whose names start with a dot are not entered. roots lists the directories.
func collectPages(args []string) (files, roots []string, err error) {
	seen := map[string]bool{}
	add := func(f string) {
		abs, aerr := filepath.Abs(f)
		if aerr != nil {
			abs = f
		}
		if !seen[abs] {
			seen[abs] = true
			files = append(files, f)
		}
	}
	for _, a := range args {
		info, serr := os.Stat(a)
		if serr != nil {
			return nil, nil, serr
		}
		if !info.IsDir() {
			add(a)
			continue
		}
		roots = append(roots, a)
		err = filepath.WalkDir(a, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.IsDir() && p != a && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".md") {
				add(p)
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return files, roots, nil
}

// findModuleRoot returns the nearest directory above any of starts that
// holds go.mod and cmd/pravo.
func findModuleRoot(starts ...string) (string, error) {
	for _, s := range starts {
		dir, err := filepath.Abs(s)
		if err != nil {
			continue
		}
		if info, err := os.Stat(dir); err == nil && !info.IsDir() {
			dir = filepath.Dir(dir)
		}
		for {
			if isFile(filepath.Join(dir, "go.mod")) && isDir(filepath.Join(dir, "cmd", "pravo")) {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", errors.New("cannot find the pravo module: run capture inside it, or pass -bin")
}

func isFile(p string) bool { i, err := os.Stat(p); return err == nil && i.Mode().IsRegular() }
func isDir(p string) bool  { i, err := os.Stat(p); return err == nil && i.IsDir() }

// prepareBinary makes a directory that holds one file, pravo: the binary
// given by -bin, or one built from ./cmd/pravo. It returns the directory and
// the lines `pravo version` prints.
func prepareBinary(ctx context.Context, bin string, starts []string, verbose bool, log io.Writer) (dir string, version []string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", "capture-bin-")
	if err != nil {
		return "", nil, nil, err
	}
	cleanup = func() { os.RemoveAll(dir) }
	target := filepath.Join(dir, "pravo")
	if bin != "" {
		abs, aerr := filepath.Abs(bin)
		info, serr := os.Stat(abs)
		switch {
		case aerr != nil || serr != nil:
			cleanup()
			return "", nil, nil, fmt.Errorf("-bin %s: no such file", bin)
		case !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0:
			cleanup()
			return "", nil, nil, fmt.Errorf("-bin %s: not an executable file", bin)
		}
		if err := os.Symlink(abs, target); err != nil {
			cleanup()
			return "", nil, nil, err
		}
	} else {
		root, rerr := findModuleRoot(append([]string{"."}, starts...)...)
		if rerr != nil {
			cleanup()
			return "", nil, nil, rerr
		}
		if verbose {
			fmt.Fprintln(log, "capture: building pravo from", root)
		}
		cmd := exec.CommandContext(ctx, "go", "build", "-o", target, "./cmd/pravo")
		cmd.Dir = root
		if out, berr := cmd.CombinedOutput(); berr != nil {
			cleanup()
			return "", nil, nil, fmt.Errorf("building pravo: %v\n%s", berr, strings.TrimSpace(string(out)))
		}
	}
	if out, verr := exec.CommandContext(ctx, target, "version").Output(); verr == nil {
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				version = append(version, l)
			}
		}
	}
	return dir, version, cleanup, nil
}

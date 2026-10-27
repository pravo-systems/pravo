// Command hclcheck runs every OpenTofu listing of the documentation through
// tofu.
//
// It reads the Markdown pages named on the command line, or the default
// directories docs and site/demos of the module, extracts every ```hcl fence,
// writes the blocks of a page into a scratch root in page order, completes the
// root where a listing shows only part of it, and runs tofu fmt -check,
// tofu init and tofu validate. It prints one row per block. See README.md.
//
//	go run ./tools/hclcheck
//	go run ./tools/hclcheck docs/migrate/m4-translate-the-specification.md
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage: hclcheck [-tofu PATH] [-j N] [-cache DIR] [-timeout D] [-keep] [-list] [-v] [PATH...]

hclcheck checks the hcl fences of Markdown pages with tofu. PATH is a Markdown
file or a directory that is searched for pages; the default is docs and
site/demos of the module.

`

// run is main with its streams and arguments passed in. It returns the exit
// status: 0 when every block is valid or fails as it is marked to, 1 when a
// block does not, 2 when hclcheck cannot do its work.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("hclcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, usage)
		flags.PrintDefaults()
	}
	tofu := flags.String("tofu", "tofu", "the tofu binary")
	jobs := flags.Int("j", 1, "pages to check at a time; 0 means one per CPU")
	cache := flags.String("cache", filepath.Join(os.TempDir(), "hclcheck-plugins"), "provider plugin cache directory")
	timeout := flags.Duration("timeout", 10*time.Minute, "longest one tofu command may take")
	keep := flags.Bool("keep", false, "keep the scratch roots and print where they are")
	list := flags.Bool("list", false, "list the blocks and the file each one is written to; run nothing")
	verbose := flags.Bool("v", false, "print the output of every failing command")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	root, err := moduleRoot()
	if err != nil {
		fmt.Fprintln(stderr, "hclcheck:", err)
		return 2
	}
	paths := flags.Args()
	if len(paths) == 0 {
		paths = []string{filepath.Join(root, "docs"), filepath.Join(root, "site", "demos")}
	}
	pages, err := collect(paths)
	if err != nil {
		fmt.Fprintln(stderr, "hclcheck:", err)
		return 2
	}
	var byPage [][]Block
	for _, p := range pages {
		src, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintln(stderr, "hclcheck:", err)
			return 2
		}
		bs, err := extract(relTo(root, p), string(src))
		if err != nil {
			fmt.Fprintln(stderr, "hclcheck:", err)
			return 2
		}
		if len(bs) > 0 {
			byPage = append(byPage, bs)
		}
	}
	if *list {
		return printList(stdout, byPage)
	}
	if _, err := exec.LookPath(*tofu); err != nil {
		fmt.Fprintf(stderr, "hclcheck: %v\n", err)
		return 2
	}
	if err := os.MkdirAll(*cache, 0o755); err != nil {
		fmt.Fprintln(stderr, "hclcheck:", err)
		return 2
	}
	base, err := os.MkdirTemp("", "hclcheck-")
	if err != nil {
		fmt.Fprintln(stderr, "hclcheck:", err)
		return 2
	}
	if *keep {
		fmt.Fprintln(stderr, "hclcheck: scratch roots in", base)
	} else {
		defer os.RemoveAll(base)
	}
	n := *jobs
	if n <= 0 {
		n = runtime.NumCPU()
	}
	r := newRunner(*tofu, *cache, *timeout)
	results := make([][]Result, len(byPage))
	var wg sync.WaitGroup
	sem := make(chan struct{}, n)
	for i := range byPage {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = r.checkPage(base, byPage[i])
		}(i)
	}
	wg.Wait()
	return report(stdout, results, *verbose)
}

// moduleRoot finds the directory that holds go.mod, from the working directory
// up.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod above the working directory: run hclcheck inside the module")
		}
		dir = parent
	}
}

func relTo(root, p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if r, err := filepath.Rel(root, abs); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(p)
}

// collect expands the paths into Markdown files, in path order.
func collect(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			out = append(out, p)
			continue
		}
		err = filepath.WalkDir(p, func(f string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && f != p && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(f, ".md") {
				out = append(out, f)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func printList(w io.Writer, pages [][]Block) int {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PAGE\tBLOCK\tFILE\tMARK")
	for _, bs := range pages {
		for _, b := range bs {
			mark := ""
			switch {
			case b.Skip != "":
				mark = "skip=" + b.Skip
			case b.Expect != "":
				mark = "expect=" + b.Expect
			case b.Excerpt:
				mark = "excerpt"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", b.Page, b.label(), strings.Join(b.Files, " + "), mark)
		}
	}
	tw.Flush()
	return 0
}

// report prints the table and the totals and returns the exit status.
func report(w io.Writer, pages [][]Result, verbose bool) int {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PAGE\tBLOCK\tFILE\tSTATUS\tNOTE")
	count := map[string]int{}
	var failed []Result
	for _, rs := range pages {
		for _, r := range rs {
			status := r.Status
			if r.Status == statusFail || r.Status == statusExpected {
				status += " " + r.Stage
			}
			count[r.Status]++
			if r.Status == statusFail {
				failed = append(failed, r)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Block.Page, r.Block.label(), r.Block.Path, status, oneLine(r.Note))
		}
	}
	tw.Flush()
	total := 0
	for _, n := range count {
		total += n
	}
	fmt.Fprintf(w, "\n%d blocks: %d ok, %d ok with stubs, %d expected errors, %d skipped, %d failed\n",
		total, count[statusOK], count[statusStubs], count[statusExpected], count[statusSkipped], count[statusFail])
	if verbose {
		for _, r := range failed {
			fmt.Fprintf(w, "\n--- %s %s (%s)\n%s\n", r.Block.Page, r.Block.label(), r.Stage, strings.TrimSpace(r.Detail))
		}
	}
	if len(failed) > 0 {
		return 1
	}
	return 0
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:157] + "..."
	}
	return s
}

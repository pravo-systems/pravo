//go:build ignore

// fixtures checks that what the migration pages embed from OpenTofu is what
// site/demos/migrate/tofu/record.sh recorded:
//
//   - the state files and the object files, which the pages keep as file= blocks
//     so that tools/capture runs without OpenTofu;
//
//   - the HCL that the pages show, which is a part of a file under tofu/;
//
//   - the OpenTofu output that the pages show, which is an excerpt of a log
//     under tofu/state/log. A line that reads "..." marks a cut.
//
//     go run site/demos/migrate/fixtures.go           # report every difference
//     go run site/demos/migrate/fixtures.go -update   # rewrite the state and object blocks
//
// Run it from the root of the module. -update only rewrites blocks that must
// equal a recorded file. An excerpt is edited by hand, because a person chooses
// the cuts.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const tofuDir = "site/demos/migrate/tofu"

// An entry ties one fenced block of a page to a recorded file.
//
//	exact   the block is the file
//	lines   the block is the lines A-B of the file ("lines:1-6")
//	excerpt the block is made of runs of lines of the file, in order, and a line
//	        "..." stands for the lines between two runs
type entry struct {
	page   string
	match  string // a substring of the opening fence line of the block
	source string // below tofu/ (HCL) or tofu/state/ (recorded files)
	mode   string
}

const (
	m1 = "docs/migrate/m1-before-you-start.md"
	m3 = "docs/migrate/m3-worked-example.md"
	m5 = "docs/migrate/m5-convert-the-state.md"
	m7 = "docs/migrate/m7-cut-over.md"
	m8 = "docs/migrate/m8-rollback.md"
	m9 = "docs/migrate/m9-pitfalls.md"
	rs = "docs/reference/state.md"
	ld = "site/demos/migrate/page.md"
)

var entries = []entry{
	// M3: two roots
	{m3, "file=platform/terraform.tfstate", "state/m3-platform.tfstate", "exact"},
	{m3, "file=gateway/terraform.tfstate", "state/m3-gateway.tfstate", "exact"},
	{m3, `title="platform/terraform.tfstate (excerpt)"`, "state/m3-platform.tfstate", "excerpt"},
	{m3, `title="gateway/terraform.tfstate (excerpt)"`, "state/m3-gateway.tfstate", "excerpt"},
	{m3, `title="platform/main.tf, lines 1-6"`, "m3/platform/main.tf", "lines:1-6"},
	{m3, `title="platform/modules/service/main.tf"`, "m3/platform/modules/service/main.tf", "exact"},
	{m3, `title="platform/main.tf, lines 8-41"`, "m3/platform/main.tf", "lines:8-41"},
	{m3, `title="gateway/main.tf"`, "m3/gateway/main.tf", "exact"},
	{m3, `title="OpenTofu 1.13.1"`, "state/log/m3-platform-apply-1.log", "excerpt"},
	{m3, `title="OpenTofu 1.13.1, exit code 2"`, "state/log/m3-platform-plan-moved.log", "excerpt"},
	{m3, `title="OpenTofu 1.13.1, exit code 0 in both roots"`, "state/log/m3-platform-plan-empty.log", "excerpt"},

	// M1
	{m1, "file=backup/platform.tfstate", "state/m1-platform.tfstate", "exact"},
	{m1, `title="tofu plan -detailed-exitcode, OpenTofu 1.13.1, exit code 2"`, "state/log/m1-plan-before.log", "excerpt"},
	{m1, `title="tofu plan -detailed-exitcode after the apply, OpenTofu 1.13.1, exit code 0"`, "state/log/m1-plan-after.log", "excerpt"},

	{m1, `title="the loop on platform, OpenTofu 1.13.1 and the OpenTofu registry"`, "state/log/m1-registry-check.log", "exact"},
	{m1, `title="the lock file of platform, OpenTofu 1.13.1"`, "state/log/m1-lock-versions.log", "exact"},

	// M5: the estate of M4 and a root with workspaces
	{m5, "file=terraform.tfstate", "state/estate.tfstate", "exact"},
	{m5, "file=banner.txt", "state/objects/banner.txt", "exact"},
	{m5, "file=out/hello.txt", "state/objects/out/hello.txt", "exact"},
	{m5, "file=out/notes/alpha.txt", "state/objects/out/notes/alpha.txt", "exact"},
	{m5, "file=out/notes/beta.txt", "state/objects/out/notes/beta.txt", "exact"},
	{m5, "file=ws/dev.tfstate", "state/ws-dev.tfstate", "exact"},
	{m5, "file=ws/prod.tfstate", "state/ws-prod.tfstate", "exact"},
	{m5, `title="tofu workspace list, OpenTofu 1.13.1"`, "state/log/ws-workspace-list.log", "excerpt"},

	// M7
	{m7, `title="the dry run, OpenTofu 1.13.1"`, "state/log/m7-state-rm-dry-run.log", "exact"},
	{m7, `title="the removal, OpenTofu 1.13.1 (excerpt)"`, "state/log/m7-state-rm.log", "excerpt"},
	{m7, `title="tofu plan -detailed-exitcode, OpenTofu 1.13.1, exit code 2 (excerpt)"`, "state/log/m7-plan-after.log", "excerpt"},

	// M8
	{m8, "file=terraform.tfstate", "state/estate.tfstate", "exact"},
	{m8, "file=banner.txt", "state/objects/banner.txt", "exact"},
	{m8, "file=out/hello.txt", "state/objects/out/hello.txt", "exact"},
	{m8, "file=out/notes/alpha.txt", "state/objects/out/notes/alpha.txt", "exact"},
	{m8, "file=out/notes/beta.txt", "state/objects/out/notes/beta.txt", "exact"},
	{m8, `title="tofu plan -detailed-exitcode, OpenTofu 1.13.1, exit code 0 (excerpt)"`, "state/log/m8-plan-estate.log", "excerpt"},
	{m8, `title="tofu import of random_pet.region ap, OpenTofu 1.13.1, exit code 1"`, "state/log/m8-import-random-pet.log", "excerpt"},
	{m8, `title="tofu state push archive/platform-final.tfstate, exit code 1"`, "state/log/m8-push-without-force.log", "excerpt"},
	{m8, `title="tofu plan -detailed-exitcode after the push -force, exit code 0 (excerpt)"`, "state/log/m8-plan-restored.log", "excerpt"},

	// M9
	{m9, "file=ok.tfstate", "state/ok.tfstate", "exact"},
	{m9, `title="the HCL that wrote ok.tfstate"`, "ok/main.tf", "excerpt"},
	{m9, "file=clash.tfstate", "state/clash.tfstate", "exact"},
	{m9, `title="the HCL that wrote clash.tfstate"`, "clash/main.tf", "excerpt"},
	{m9, "file=clash-fixed.tfstate", "state/clash-fixed.tfstate", "exact"},
	{m9, "file=taint.tfstate", "state/taint.tfstate", "exact"},
	{m9, "file=builtin.tfstate", "state/builtin.tfstate", "exact"},
	{m9, `title="the HCL that wrote builtin.tfstate"`, "builtin/main.tf", "excerpt"},

	// Reference
	{rs, "file=migrate/terraform.tfstate", "state/ref.tfstate", "exact"},
	{rs, `title="the HCL that wrote terraform.tfstate"`, "ref/main.tf", "excerpt"},

	// The landing band
	{ld, "file=terraform.tfstate", "state/landing.tfstate", "exact"},
	{ld, `title="main.tf"`, "landing/main.tf", "exact"},
	{ld, "file=motd.txt", "state/objects/motd.txt", "exact"},
	{ld, "file=notes/alpha.txt", "state/objects/notes/alpha.txt", "exact"},
	{ld, "file=notes/beta.txt", "state/objects/notes/beta.txt", "exact"},
}

type fence struct {
	open, close int // line indexes of the opening and closing fence
}

// fences finds the fenced blocks of a Markdown page.
func fences(lines []string) []fence {
	var out []fence
	for i := 0; i < len(lines); i++ {
		n := backticks(lines[i])
		if n < 3 {
			continue
		}
		j := i + 1
		for ; j < len(lines); j++ {
			if m := backticks(lines[j]); m >= n && strings.TrimSpace(lines[j]) == strings.Repeat("`", m) {
				break
			}
		}
		out = append(out, fence{i, j})
		i = j
	}
	return out
}

func backticks(l string) int {
	n := 0
	for n < len(l) && l[n] == '`' {
		n++
	}
	return n
}

func trimEnd(s string) string { return strings.TrimRight(s, "\n") }

func read(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fixtures:", err)
		os.Exit(2)
	}
	return string(b)
}

// excerpt reports whether the runs of block, separated by lines "...", appear
// in order as runs of consecutive lines of src. A leading or trailing blank
// line of src does not count.
func excerpt(block, src []string) bool {
	var runs [][]string
	var cur []string
	for _, l := range block {
		if strings.TrimSpace(l) == "..." {
			if len(cur) > 0 {
				runs = append(runs, cur)
			}
			cur = nil
			continue
		}
		cur = append(cur, l)
	}
	if len(cur) > 0 {
		runs = append(runs, cur)
	}
	pos := 0
	for _, run := range runs {
		found := -1
		for i := pos; i+len(run) <= len(src); i++ {
			ok := true
			for k := range run {
				if strings.TrimRight(src[i+k], " \t") != strings.TrimRight(run[k], " \t") {
					ok = false
					break
				}
			}
			if ok {
				found = i + len(run)
				break
			}
		}
		if found < 0 {
			return false
		}
		pos = found
	}
	return true
}

func main() {
	update := flag.Bool("update", false, "rewrite the blocks that must equal a recorded file")
	flag.Parse()

	pages := map[string][]string{}
	dirty := map[string]bool{}
	bad := 0
	for _, e := range entries {
		lines, ok := pages[e.page]
		if !ok {
			lines = strings.Split(read(e.page), "\n")
			pages[e.page] = lines
		}
		var f *fence
		for _, c := range fences(lines) {
			if strings.Contains(lines[c.open], e.match) {
				c := c
				f = &c
				break
			}
		}
		if f == nil {
			fmt.Printf("%s: no block with %s\n", e.page, e.match)
			bad++
			continue
		}
		path := filepath.Join(tofuDir, e.source)
		srcText := trimEnd(read(path))
		block := lines[f.open+1 : f.close]
		var want []string
		switch {
		case e.mode == "exact":
			want = strings.Split(srcText, "\n")
		case strings.HasPrefix(e.mode, "lines:"):
			var a, b int
			ab := strings.Split(strings.TrimPrefix(e.mode, "lines:"), "-")
			a, _ = strconv.Atoi(ab[0])
			b, _ = strconv.Atoi(ab[1])
			want = strings.Split(srcText, "\n")[a-1 : b]
		case e.mode == "excerpt":
			if !excerpt(block, strings.Split(srcText, "\n")) {
				fmt.Printf("%s:%d: the block %s is not an excerpt of %s\n", e.page, f.open+1, e.match, e.source)
				bad++
			}
			continue
		}
		if strings.Join(block, "\n") == strings.Join(want, "\n") {
			continue
		}
		if *update && e.mode == "exact" {
			lines = append(append(append([]string{}, lines[:f.open+1]...), want...), lines[f.close:]...)
			pages[e.page] = lines
			dirty[e.page] = true
			fmt.Printf("%s: rewrote the block %s from %s\n", e.page, e.match, e.source)
			continue
		}
		fmt.Printf("%s:%d: the block %s differs from %s\n", e.page, f.open+1, e.match, e.source)
		bad++
	}
	for p := range dirty {
		if err := os.WriteFile(p, []byte(strings.Join(pages[p], "\n")), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "fixtures:", err)
			os.Exit(2)
		}
	}
	if bad > 0 {
		fmt.Printf("fixtures: %d of %d blocks differ\n", bad, len(entries))
		os.Exit(1)
	}
	fmt.Printf("fixtures: %d blocks match what record.sh recorded\n", len(entries))
}

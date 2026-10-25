//go:build ignore

// derive writes site/data/learn.json for the landing page: the 14 lessons of
// Track A and the 10 lessons of Track B, each with its id, page, title, minutes
// and one-sentence idea, and the totals that the start band prints.
//
// The values come from the front matter of docs/learn/<id>-*.md: title,
// minutes and description. A lesson whose page does not exist yet takes its
// values from the page map, held in the table below, and is marked
// "from": "page map" in the data. Run it again when the pages change.
//
//	go run site/demos/learn/derive.go          # write site/data/learn.json
//	go run site/demos/learn/derive.go -check   # fail when it is stale
//
// Run it from the root of the module.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	docsDir = "docs/learn"
	outFile = "site/data/learn.json"
)

// fallback is the page map: slug, title, minutes, idea. It is used for a page that does not exist yet.
var fallback = [][4]string{
	{"a01-your-data-is-already-cue", "Your data is already CUE", "6", "A CUE file may contain plain JSON. Everything else is convenience."},
	{"a02-a-value-can-be-a-type", "A value can be a type", "8", "A field can hold a type, a range or a concrete value, from general to specific."},
	{"a03-writing-a-field-twice", "Writing a field twice", "10", "Repeating a field adds detail. The copies must agree, and a disagreement is an error."},
	{"a04-order-does-not-matter", "Order does not matter", "7", "Lines and files can come in any order. The result is the same."},
	{"a05-constraints-narrow-a-value", "Constraints narrow a value", "9", "A constraint is a value written on the field. Together they are the type."},
	{"a06-choices-and-defaults", "Choices and defaults", "9", "Offer choices, mark one as the default, and select it from the command line."},
	{"a07-required-optional-regular", "Required, optional, regular", "8", "Decide for each field whether it must be supplied, may be supplied, or is a plain field."},
	{"a08-definitions-reject-typos", "Definitions reject typos", "10", "A definition is a closed shape. A value built from it may hold only declared fields."},
	{"a09-patterns", "One rule for every entry", "8", "A pattern states one rule for every entry of a collection."},
	{"a10-comprehensions", "Generating fields from data", "10", "A for builds fields from other data. Names come from keys, so choose stable keys."},
	{"a11-references-let-hidden", "Naming a value once", "7", "A field can refer to another. Helper values live in let or hidden fields."},
	{"a12-conditions-and-strings", "Conditions and strings", "7", "An if includes a field only when a condition holds. A condition needs a concrete value."},
	{"a13-packages-and-the-standard-library", "Packages and the standard library", "9", "A directory is a package. The standard library brings validators and functions."},
	{"a14-project-three-environments", "Project, three environments from one definition", "15", "One definition produces three environments, and a fourth is refused."},
	{"b01-providers-and-resources", "Providers and resources", "10", "A provider is a struct built from a generated definition. A resource is built through it."},
	{"b02-reading-a-plan", "Reading a plan", "10", "A plan is a diff with a legend: glyphs, unknown values and the replacement marker."},
	{"b03-apply-outputs-state", "Apply, outputs, state", "10", "State is a graph with one record per resource."},
	{"b04-inputs-tags-environments", "Inputs, tags, environments", "12", "A tag and a constraint select the environment, and the state path follows from it."},
	{"b05-many-resources-from-one-rule", "Many resources from one rule", "10", "A comprehension produces resources. Each key becomes the last element of an address."},
	{"b06-modules-providers-credentials", "Modules, providers, credentials", "15", "A module is a definition that contains resources. A provider is passed in as an input."},
	{"b07-changing-live-infrastructure", "Changing live infrastructure safely", "12", "Save a plan, apply that plan or nothing, and read the refusal when it is stale."},
	{"b08-renames-moves-imports", "Renames, moves, imports", "12", "The address is the identity of a resource. $from moves a record and $import adopts an object."},
	{"b09-drift", "Drift", "10", "Change a resource outside pravo, find it with pravo drift, and reconcile it two ways."},
	{"b10-policy", "Policy on providers and on plans", "15", "A constraint on a provider binds its resources. A policy node judges what a plan does."},
}

type stop struct {
	ID      string `json:"id"`
	Page    string `json:"page"` // path from the site root, ending in .html
	Title   string `json:"title"`
	Short   string `json:"short"` // the name under the stop
	Minutes int    `json:"minutes"`
	Idea    string `json:"idea"`
	From    string `json:"from"` // front matter or page map
}

type track struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Lessons int    `json:"lessons"`
	Minutes int    `json:"minutes"`
	First   string `json:"first"`
	Stops   []stop `json:"stops"`
}

type total struct {
	Lessons int `json:"lessons"`
	Minutes int `json:"minutes"`
}

// extra counts the pages of Learn that are not lessons: the labs, the
// project of Track B and the companion pages.
type extra struct {
	Pages      int `json:"pages"`
	Minutes    int `json:"minutes"`
	Labs       int `json:"labs"`
	Project    int `json:"project"`
	Companions int `json:"companions"`
}

type data struct {
	Source string `json:"source"`
	Found  int    `json:"found"`
	Total  total  `json:"total"`
	Extra  extra  `json:"extra"`
	A      track  `json:"a"`
	B      track  `json:"b"`
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "derive: "+format+"\n", a...)
	os.Exit(1)
}

// frontMatter reads the top-level scalar keys of the front matter of a page.
func frontMatter(path string) (map[string]string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	fm := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for sc.Scan() {
		l := sc.Text()
		if strings.TrimSpace(l) == "---" {
			n++
			if n == 2 {
				break
			}
			continue
		}
		if n != 1 || l == "" || l[0] == ' ' || l[0] == '\t' || l[0] == '-' {
			continue
		}
		i := strings.Index(l, ":")
		if i < 0 {
			continue
		}
		v := strings.TrimSpace(l[i+1:])
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		fm[l[:i]] = v
	}
	return fm, n == 2
}

func short(title string) string {
	if strings.HasPrefix(title, "Project,") {
		return "Project"
	}
	return title
}

func build() data {
	var d data
	found := 0
	mk := func(letter string, n int) track {
		t := track{}
		for _, fb := range fallback {
			if !strings.HasPrefix(fb[0], strings.ToLower(letter)) {
				continue
			}
			s := stop{ID: strings.ToUpper(letter) + strconv.Itoa(len(t.Stops)+1), Page: "learn/" + fb[0] + ".html", Title: fb[1], From: "page map", Idea: fb[3]}
			s.Minutes, _ = strconv.Atoi(fb[2])
			if fm, ok := frontMatter(filepath.Join(docsDir, fb[0]+".md")); ok {
				found++
				s.From = "front matter"
				if v := fm["title"]; v != "" {
					s.Title = v
				}
				if v, err := strconv.Atoi(fm["minutes"]); err == nil {
					s.Minutes = v
				}
				if v := fm["description"]; v != "" {
					s.Idea = v
				}
			}
			s.Short = short(s.Title)
			t.Stops = append(t.Stops, s)
			t.Minutes += s.Minutes
		}
		t.Lessons = len(t.Stops)
		t.First = t.Stops[0].Page
		return t
	}
	d.A = mk("a", 14)
	d.A.Name, d.A.Title = "Track A", "CUE from zero"
	d.B = mk("b", 10)
	d.B.Name, d.B.Title = "Track B", "pravo"
	if d.A.Lessons != 14 || d.B.Lessons != 10 {
		die("the page map holds %d and %d lessons, the landing page needs 14 and 10", d.A.Lessons, d.B.Lessons)
	}
	d.Found = found
	d.Total = total{d.A.Lessons + d.B.Lessons, d.A.Minutes + d.B.Minutes}
	d.Extra = extraPages(d)
	d.Source = fmt.Sprintf("front matter of %d of %d pages in %s; the page map for the rest", found, d.Total.Lessons, docsDir)
	return d
}

// extraPages reads every page of docs/learn that is not the index and not a
// lesson of either track, and sorts it into labs, project and companions.
func extraPages(d data) extra {
	lesson := map[string]bool{}
	for _, fb := range fallback {
		lesson[fb[0]+".md"] = true
	}
	var e extra
	entries, err := os.ReadDir(docsDir)
	if err != nil {
		die("%v", err)
	}
	for _, f := range entries {
		name := f.Name()
		if f.IsDir() || !strings.HasSuffix(name, ".md") || name == "index.md" || lesson[name] || strings.HasPrefix(name, "_") {
			continue
		}
		fm, ok := frontMatter(filepath.Join(docsDir, name))
		if !ok {
			die("%s has no front matter", name)
		}
		if fm["redirect"] != "" {
			continue
		}
		m, err := strconv.Atoi(fm["minutes"])
		if err != nil {
			die("%s has no minutes", name)
		}
		switch {
		case strings.HasPrefix(name, "lab"):
			e.Labs++
		case regexp.MustCompile(`^b\d\d-project`).MatchString(name):
			e.Project++
		case regexp.MustCompile(`^c\d-`).MatchString(name), name == "cue-cheat-sheet.md":
			e.Companions++
		default:
			die("%s is neither a lesson, a lab, a project nor a companion page; teach site/demos/learn/derive.go about it", name)
		}
		e.Pages++
		e.Minutes += m
	}
	return e
}

func main() {
	check := flag.Bool("check", false, "fail when site/data/learn.json is stale; write nothing")
	flag.Parse()
	d := build()
	enc, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		die("%v", err)
	}
	enc = append(enc, '\n')
	if *check {
		old, _ := os.ReadFile(outFile)
		if string(old) != string(enc) {
			die("%s is stale; run go run site/demos/learn/derive.go", outFile)
		}
		fmt.Println("derive: learn is current")
		return
	}
	if err := os.WriteFile(outFile, enc, 0o644); err != nil {
		die("%v", err)
	}
	fmt.Printf("derive: %s written (%d stops, %d minutes; %d pages read from front matter)\n", outFile, d.Total.Lessons, d.Total.Minutes, d.Found)
}

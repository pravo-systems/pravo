//go:build ignore

// facts reads three values from the sources of this repository and writes
// site/data/facts.json, which the migration demo embeds with {{data}} tokens.
// None of them is typed in a page.
//
//	go run site/demos/facts/facts.go          # from the root of the module
//	go run site/demos/facts/facts.go -check   # fail when the file is stale
//
// What it reads:
//
//	state_format  the Terraform state format that internal/tfstate accepts
//	state_since   the first Terraform release that writes that format
//	protocols     the provider plugin protocol versions of docs/reference/compatibility.md
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
)

type facts struct {
	StateFormat int    `json:"state_format"`
	StateSince  string `json:"state_since"`
	Protocols   string `json:"protocols"`
}

var check = flag.Bool("check", false, "fail when site/data/facts.json differs from the sources; write nothing")

func main() {
	flag.Parse()
	var f facts
	f.StateFormat, f.StateSince = stateFormat()
	f.Protocols = protocols()
	out, _ := json.MarshalIndent(f, "", "  ")
	out = append(out, '\n')
	if *check {
		old, _ := os.ReadFile("site/data/facts.json")
		if string(old) != string(out) {
			die("site/data/facts.json is stale; run go run site/demos/facts/facts.go from the root of the module\n--- file\n%s--- sources\n%s", old, out)
		}
		fmt.Println("facts: site/data/facts.json is current")
		return
	}
	if err := os.MkdirAll("site/data", 0o755); err != nil {
		die("%v", err)
	}
	if err := os.WriteFile("site/data/facts.json", out, 0o644); err != nil {
		die("%v", err)
	}
	fmt.Printf("facts: state format %d (Terraform %s or newer), plugin protocols %s\n", f.StateFormat, f.StateSince, f.Protocols)
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "facts: "+format+"\n", a...)
	os.Exit(1)
}

// stateFormat reads the state format that internal/tfstate accepts and the
// Terraform release from which it is written.
func stateFormat() (int, string) {
	b, err := os.ReadFile("internal/tfstate/tfstate.go")
	if err != nil {
		die("%v", err)
	}
	m := regexp.MustCompile(`f\.Version != (\d+)`).FindSubmatch(b)
	if m == nil {
		die("internal/tfstate/tfstate.go: no check of f.Version found")
	}
	n, _ := strconv.Atoi(string(m[1]))
	since := regexp.MustCompile(`Terraform (\d+\.\d+) or newer`).FindSubmatch(b)
	if since == nil {
		die("internal/tfstate/tfstate.go: the error for an unsupported format does not name the Terraform release")
	}
	return n, string(since[1])
}

// protocols reads the provider plugin protocol versions from the table of
// docs/reference/compatibility.md.
func protocols() string {
	b, err := os.ReadFile("docs/reference/compatibility.md")
	if err != nil {
		die("%v", err)
	}
	m := regexp.MustCompile(`(?m)^\| Provider plugin protocol \| ([^|]+?) \|`).FindSubmatch(b)
	if m == nil {
		die("docs/reference/compatibility.md: no row \"Provider plugin protocol\"")
	}
	return string(m[1])
}

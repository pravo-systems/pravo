package main

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var (
	pravoVersionRe = regexp.MustCompile(`(\d+)\.(\d+)`)
	cueDepRe       = regexp.MustCompile(`(?m)^\s*dep\s+cuelang\.org/go\s+v(\d+)\.(\d+)`)
)

// detectVersions asks a pravo binary for its version and for the CUE version
// it was built with. The results are "X.Y" strings. The pravo version comes
// from `pravo version`; the CUE version from `go version -m`, which reads the
// build information of the binary.
func detectVersions(bin string) (pravo, cue string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		return "", "", fmt.Errorf("%s version: %w", bin, err)
	}
	if m := pravoVersionRe.FindStringSubmatch(strings.TrimSpace(string(out))); m != nil {
		pravo = m[1] + "." + m[2]
	}
	if goOut, gerr := exec.CommandContext(ctx, "go", "version", "-m", bin).Output(); gerr == nil {
		if m := cueDepRe.FindStringSubmatch(string(goOut)); m != nil {
			cue = m[1] + "." + m[2]
		}
	}
	return pravo, cue, nil
}

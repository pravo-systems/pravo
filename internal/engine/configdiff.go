package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pravo-systems/pravo/internal/config"
)

// configChangedReason is the reason that a plan gives for being stale when the
// configuration is not the one it was made from.
const configChangedReason = "the configuration changed since the plan was made"

// maxConfigFilesNamed is the most files that the reason names; the rest are counted.
const maxConfigFilesNamed = 6

// ConfigChange says why the configuration of root is not the one that pf was made
// from, or "" when it is. When the plan records its files, the reason names the
// ones that differ: a changed file, a file that was added and one that is gone.
func ConfigChange(root *config.Root, pf *PlanFile) string {
	fp, err := root.Fingerprint()
	if err != nil || fp == pf.ConfigFingerprint {
		return ""
	}
	if len(pf.ConfigFiles) == 0 {
		return configChangedReason
	}
	now, err := root.FileHashes()
	if err != nil {
		return configChangedReason
	}
	return configDiffReason(pf.ConfigFiles, now)
}

// configDiffReason compares the files that a plan was made from with the files
// now, and writes the reason that names the differences.
func configDiffReason(saved, now map[string]string) string {
	var changed, added, removed []string
	for name, h := range now {
		old, ok := saved[name]
		switch {
		case !ok:
			added = append(added, name)
		case old != h:
			changed = append(changed, name)
		}
	}
	for name := range saved {
		if _, ok := now[name]; !ok {
			removed = append(removed, name)
		}
	}
	var parts []string
	for _, group := range []struct {
		names []string
		verb  string
	}{{changed, "changed"}, {added, "added"}, {removed, "removed"}} {
		sort.Strings(group.names)
		for _, n := range group.names {
			parts = append(parts, n+" "+group.verb)
		}
	}
	if len(parts) == 0 {
		return configChangedReason
	}
	if len(parts) > maxConfigFilesNamed {
		rest := len(parts) - maxConfigFilesNamed
		parts = append(parts[:maxConfigFilesNamed], fmt.Sprintf("and %d more", rest))
	}
	return configChangedReason + " (" + strings.Join(parts, ", ") + ")"
}

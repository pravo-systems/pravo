package engine

import (
	"strings"
	"testing"
)

func TestConfigDiffReasonNamesTheFilesThatDiffer(t *testing.T) {
	saved := map[string]string{"main.cue": "aaaaaaaaaaaa", "cue.mod/module.cue": "bbbbbbbbbbbb", "gone.cue": "cccccccccccc", "same.cue": "dddddddddddd"}
	now := map[string]string{"main.cue": "000000000000", "cue.mod/module.cue": "bbbbbbbbbbbb", "extra.cue": "eeeeeeeeeeee", "same.cue": "dddddddddddd"}
	want := "the configuration changed since the plan was made (main.cue changed, extra.cue added, gone.cue removed)"
	if got := configDiffReason(saved, now); got != want {
		t.Errorf("%s\nwant %s", got, want)
	}
	// The module file is a file like any other.
	now["main.cue"] = "aaaaaaaaaaaa"
	now["cue.mod/module.cue"] = "111111111111"
	if got := configDiffReason(saved, now); !strings.Contains(got, "cue.mod/module.cue changed") {
		t.Errorf("%s", got)
	}
}

// Files are named up to a limit, and the rest are counted.
func TestConfigDiffReasonCountsTheFilesItDoesNotName(t *testing.T) {
	saved, now := map[string]string{}, map[string]string{}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		saved[n+".cue"], now[n+".cue"] = "111111111111", "222222222222"
	}
	got := configDiffReason(saved, now)
	if !strings.HasSuffix(got, "f.cue changed, and 2 more)") || strings.Contains(got, "g.cue") {
		t.Errorf("%s", got)
	}
}

// Without anything to compare, or where the files are the same, the reason is the
// plain one.
func TestConfigDiffReasonWithoutADifferenceIsThePlainReason(t *testing.T) {
	if got := configDiffReason(nil, map[string]string{"a.cue": "111111111111"}); !strings.Contains(got, "a.cue added") {
		t.Errorf("%s", got)
	}
	if got := configDiffReason(map[string]string{"a.cue": "1"}, map[string]string{"a.cue": "1"}); got != configChangedReason {
		t.Errorf("%s", got)
	}
}

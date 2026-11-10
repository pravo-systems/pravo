package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/env"
)

func TestPravoPlanComesFromTheEnvironment(t *testing.T) {
	old := planMode
	defer func() { planMode = old }()
	if err := applyPlanMode(env.New([]string{"PRAVO_PLAN=quick"})); err == nil {
		t.Errorf("an unknown value was accepted")
	}
	for v, want := range map[string]string{"": "", "memo": "", "full": "full", "check": "check"} {
		if err := applyPlanMode(env.New([]string{"PRAVO_PLAN=" + v})); err != nil || planMode != want {
			t.Errorf("PRAVO_PLAN=%s: %v, mode %q", v, err, planMode)
		}
	}
}

// memoEnv is an environment whose cache and configuration directories are
// below dir.
func memoEnv(dir string) env.Env {
	return env.Map(map[string]string{
		"HOME": dir, "USERPROFILE": dir, "LocalAppData": filepath.Join(dir, "cache"),
		"XDG_CACHE_HOME": filepath.Join(dir, "cache"), "PRAVO_CONFIG_DIR": filepath.Join(dir, "config"),
	})
}

// The memo of a root and an environment is one file in the cache directory of
// the user, and its key is in the configuration directory: made once, readable
// by the user alone, and not in the file.
func TestThePlanMemoOfARoot(t *testing.T) {
	old := planMode
	defer func() { planMode = old }()
	dir := t.TempDir()
	e := memoEnv(dir)
	root := &config.Root{Dir: filepath.Join(dir, "estate"), Environment: "dev"}

	planMode = ""
	m := planMemo(e, root)
	if m == nil || m.Check || len(m.Key) != 32 {
		t.Fatalf("the memo is %+v", m)
	}
	if !strings.HasPrefix(m.Path, filepath.Join(dir, "cache")) {
		t.Errorf("the memo file is %s, outside the cache directory", m.Path)
	}
	keyFile := filepath.Join(dir, "config", "plan-memo.key")
	info, err := os.Stat(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("the key file has mode %v, want 0600", info.Mode().Perm())
	}
	again := planMemo(e, root)
	if again == nil || string(again.Key) != string(m.Key) || again.Path != m.Path {
		t.Errorf("the second command has another memo: %+v, %+v", again, m)
	}
	for name, other := range map[string]*config.Root{
		"another environment": {Dir: root.Dir, Environment: "prod"},
		"another root":        {Dir: filepath.Join(dir, "other"), Environment: "dev"},
	} {
		if o := planMemo(e, other); o == nil || o.Path == m.Path {
			t.Errorf("%s has the memo file of this one", name)
		}
	}

	planMode = "check"
	if c := planMemo(e, root); c == nil || !c.Check {
		t.Errorf("PRAVO_PLAN=check: the memo is %+v", c)
	}
	planMode = "full"
	if f := planMemo(e, root); f != nil {
		t.Errorf("PRAVO_PLAN=full: the plan has a memo")
	}

	// Without a key that can be read, and without a place for either file, a
	// plan has no memo.
	planMode = ""
	if err := os.WriteFile(keyFile, []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b := planMemo(e, root); b != nil {
		t.Errorf("a key file that holds no key gave the memo %+v", b)
	}
	if n := planMemo(env.Map(nil), root); n != nil {
		t.Errorf("an environment without directories gave the memo %+v", n)
	}
}

// A memo that no command has used for a month is removed when another is
// used, and the one that is used is kept, whatever its age.
func TestPlanMemosThatNothingUsesAreRemoved(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-planMemoAge - time.Hour)
	for _, name := range []string{"used.json", "stale.json", "fresh.json", "stale.json.123"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		at := old
		if name == "fresh.json" {
			at = now.Add(-time.Hour)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	tidyPlanMemos(dir, filepath.Join(dir, "used.json"), now)
	var left []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		left = append(left, ent.Name())
	}
	if strings.Join(left, " ") != "fresh.json used.json" {
		t.Errorf("the directory holds %v, want the memo that was used and the one of an hour ago", left)
	}
	// A directory that does not exist is no error.
	tidyPlanMemos(filepath.Join(dir, "none"), filepath.Join(dir, "none", "x.json"), now)
}

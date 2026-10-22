package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/pravo-systems/pravo/skills"
)

func TestSkillInstallWritesTheEmbeddedSkill(t *testing.T) {
	dir := t.TempDir()
	out, code := runPravo(t, t.TempDir(), "skill", "install", dir)
	if code != 0 || !strings.Contains(out, "Installed the pravo skill") {
		t.Fatalf("install: %d %q", code, out)
	}
	files, err := skillFiles()
	if err != nil || len(files) < 50 {
		t.Fatalf("the embedded skill has %d files: %v", len(files), err)
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(dir, "pravo", filepath.FromSlash(f)))
		if err != nil {
			t.Fatalf("%s was not written: %v", f, err)
		}
		want, _ := fs.ReadFile(skills.FS, path.Join(skills.Dir, f))
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from the embedded copy", f)
		}
	}
	if v, _ := os.ReadFile(filepath.Join(dir, "pravo", "VERSION")); strings.TrimSpace(string(v)) != Version {
		t.Errorf("VERSION is %q, want %q", v, Version)
	}
}

func TestSkillInstallReplacesAnEarlierCopy(t *testing.T) {
	dir := t.TempDir()
	if _, code := runPravo(t, t.TempDir(), "skill", "install", dir); code != 0 {
		t.Fatal("first install")
	}
	stale := filepath.Join(dir, "pravo", "references", "gone.md")
	changed := filepath.Join(dir, "pravo", "SKILL.md")
	os.WriteFile(stale, []byte("old"), 0o644)
	os.WriteFile(changed, []byte("---\nname: pravo\n---\nedited\n"), 0o644)
	if out, code := runPravo(t, t.TempDir(), "skill", "install", dir); code != 0 {
		t.Fatalf("second install: %d %s", code, out)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("a file of the earlier copy that the skill no longer has was kept")
	}
	want, _ := fs.ReadFile(skills.FS, path.Join(skills.Dir, "SKILL.md"))
	if got, _ := os.ReadFile(changed); !bytes.Equal(got, want) {
		t.Error("SKILL.md was not replaced")
	}
	if _, err := os.Stat(filepath.Join(dir, "pravo.new")); err == nil {
		t.Error("the staging directory was left behind")
	}
}

func TestSkillInstallDoesNotTouchAnotherDirectory(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "pravo")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "notes.txt"), []byte("mine"), 0o644)
	out, code := runPravo(t, t.TempDir(), "skill", "install", dir)
	if code != 5 || !strings.Contains(out, "not the pravo skill") {
		t.Fatalf("install over a foreign directory: %d %q", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(other, "notes.txt")); string(b) != "mine" {
		t.Error("the foreign directory was changed")
	}
}

func TestSkillInstallDefaultsToTheSkillsDirectoryOfClaude(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	out, code := runPravo(t, t.TempDir(), "--json", "skill", "install")
	if code != 0 {
		t.Fatalf("install: %d %s", code, out)
	}
	var res struct {
		Path    string `json:"path"`
		Files   int    `json:"files"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if res.Path != filepath.Join(cfg, "skills", "pravo") || res.Files < 50 || res.Version != Version {
		t.Errorf("result %+v", res)
	}
	if _, err := os.Stat(filepath.Join(cfg, "skills", "pravo", "SKILL.md")); err != nil {
		t.Error(err)
	}
}

func TestSkillPrint(t *testing.T) {
	out, code := runPravo(t, t.TempDir(), "skill", "print")
	// A checkout with other line endings is still the skill.
	if code != 0 || !strings.HasPrefix(strings.ReplaceAll(out, "\r\n", "\n"), "---\nname: pravo\n") {
		t.Fatalf("print: %d %.60q", code, out)
	}
	out, code = runPravo(t, t.TempDir(), "skill", "print", "references/reference/plan.md")
	if code != 0 || !strings.Contains(out, "# ") {
		t.Fatalf("print a reference: %d %.60q", code, out)
	}
	for _, bad := range []string{"../go.mod", "/etc/passwd", "references/nope.md"} {
		if out, code := runPravo(t, t.TempDir(), "skill", "print", bad); code == 0 {
			t.Errorf("print %q succeeded: %.60q", bad, out)
		}
	}
	list, code := runPravo(t, t.TempDir(), "skill", "list")
	if code != 0 || !strings.Contains(list, "SKILL.md\n") || !strings.Contains(list, "references/INDEX.md\n") {
		t.Fatalf("list: %d %.80q", code, list)
	}
}

// The guide names commands and flags. Each of them must exist, so that a
// command that is renamed or removed makes this test fail until SKILL.md says so.
func TestSkillNamesOnlyRealCommandsAndFlags(t *testing.T) {
	data, err := fs.ReadFile(skills.FS, path.Join(skills.Dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	root := newRootCmd()
	checked := 0
	for _, line := range pravoLines(string(data)) {
		checked++
		if msg := checkPravoLine(root, line); msg != "" {
			t.Errorf("SKILL.md: %q: %s", line, msg)
		}
	}
	if checked < 25 {
		t.Errorf("only %d command lines found in SKILL.md: the extraction is broken", checked)
	}
}

var inlineRe = regexp.MustCompile("`(pravo [^`]*)`")

// pravoLines returns the text of each command that SKILL.md shows: the lines of
// the code blocks that start with pravo, and the inline code that does.
func pravoLines(md string) []string {
	var out []string
	inFence := false
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			if strings.HasPrefix(line, "pravo ") {
				out = append(out, cutShell(line))
			}
			continue
		}
		for _, m := range inlineRe.FindAllStringSubmatch(line, -1) {
			out = append(out, cutShell(m[1]))
		}
	}
	return out
}

// cutShell ends a command at a comment, a pipe, a redirect or a list of
// alternatives written with a bar.
func cutShell(s string) string {
	if i := strings.IndexAny(s, "#|>;"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

var placeholderRe = regexp.MustCompile(`^[A-Z][A-Z_0-9.]*$`)

func checkPravoLine(root *cobra.Command, line string) string {
	toks := strings.Fields(line)[1:] // after "pravo"
	cmd := root
	var flags []string
	descend := true
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch {
		case strings.HasPrefix(t, "-"):
			flags = append(flags, t)
		case placeholderRe.MatchString(t):
			descend = false // an address, a file or COMMAND
		case descend:
			if sub := findSub(cmd, t); sub != nil {
				cmd = sub
			} else if cmd.HasSubCommands() && cmd.Name() != "pravo" || cmd == root && !strings.ContainsAny(t, "./") {
				return "unknown command " + t
			} else {
				descend = false
			}
		}
	}
	for _, f := range flags {
		name := strings.TrimLeft(f, "-")
		if i := strings.IndexByte(name, '='); i >= 0 {
			name = name[:i]
		}
		if name == "help" || name == "h" || name == "" || (!strings.HasPrefix(f, "--") && len(name) > 1) {
			continue // -vv and the like
		}
		if lookupFlag(cmd, name) == nil {
			return "no flag " + f + " on " + cmd.CommandPath()
		}
	}
	return ""
}

func findSub(cmd *cobra.Command, name string) *cobra.Command {
	for _, c := range cmd.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func lookupFlag(cmd *cobra.Command, name string) *pflag.Flag {
	for _, fs := range []*pflag.FlagSet{cmd.Flags(), cmd.InheritedFlags(), cmd.PersistentFlags()} {
		if f := fs.Lookup(name); f != nil {
			return f
		}
		if len(name) == 1 {
			if f := fs.ShorthandLookup(name); f != nil {
				return f
			}
		}
	}
	return nil
}

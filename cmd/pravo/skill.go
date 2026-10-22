package main

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/skills"
)

func skillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Install or print the skill that teaches an agent to use pravo",
		Long: `The skill is a directory for an AI agent: SKILL.md, a short guide, and
references/, the pages of the pravo documentation as Markdown. It is part of the
binary, so the skill always matches the version that installed it.`,
		Args: noArgs,
	}
	cmd.AddCommand(skillInstallCmd(), skillPrintCmd(), skillListCmd())
	return cmd
}

func skillInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install [dir]",
		Short: "Write the skill to a directory of skills",
		Long: `Writes the skill to DIR/pravo. DIR defaults to the skills directory of Claude:
$CLAUDE_CONFIG_DIR/skills, or ~/.claude/skills. An earlier copy of the skill in
that place is replaced. A directory named pravo that holds something else is
not touched.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := skillsDir(args)
			if err != nil {
				return err
			}
			target, n, err := installSkill(dir)
			if err != nil {
				return err
			}
			if flagJSON {
				return emitJSON(map[string]any{"path": target, "files": n, "version": Version})
			}
			out.Printf("Installed the pravo skill for pravo %s: %d files in %s\n", Version, n, target)
			return nil
		},
	}
}

func skillPrintCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "print [file]",
		Short: "Print SKILL.md, or another file of the skill",
		Long: `Prints SKILL.md, the short guide, or the file of the skill that is named, such as
references/reference/plan.md. "pravo skill list" lists the files.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "SKILL.md"
			if len(args) == 1 {
				name = path.Clean(filepath.ToSlash(args[0]))
			}
			if name == ".." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
				return fmt.Errorf("%q is not a file of the skill", args[0])
			}
			data, err := fs.ReadFile(skills.FS, path.Join(skills.Dir, name))
			if err != nil {
				return fmt.Errorf("%q is not a file of the skill; \"pravo skill list\" lists them", name)
			}
			_, err = os.Stdout.Write(data)
			return err
		},
	}
}

func skillListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the files of the skill",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			files, err := skillFiles()
			if err != nil {
				return err
			}
			if flagJSON {
				return emitJSON(map[string]any{"version": Version, "files": files})
			}
			for _, f := range files {
				fmt.Println(f)
			}
			return nil
		},
	}
}

// skillFiles lists the embedded files, as paths below the skill directory.
func skillFiles() ([]string, error) {
	var files []string
	err := fs.WalkDir(skills.FS, skills.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files = append(files, strings.TrimPrefix(p, skills.Dir+"/"))
		return nil
	})
	sort.Strings(files)
	return files, err
}

// skillsDir is the directory the skill goes to: the argument, or the skills
// directory of Claude.
func skillsDir(args []string) (string, error) {
	if len(args) == 1 {
		return filepath.Abs(args[0])
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "skills"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home directory to find the skills directory in: name one, as in \"pravo skill install DIR\"")
	}
	return filepath.Join(home, ".claude", "skills"), nil
}

// installSkill writes the skill to dir/pravo and returns that path and the
// number of files. The new copy is written beside the old one and replaces it,
// so a failure leaves the old copy whole.
func installSkill(dir string) (string, int, error) {
	target := filepath.Join(dir, skills.Dir)
	if st, err := os.Stat(target); err == nil {
		if !st.IsDir() {
			return "", 0, fmt.Errorf("%s is a file, not a directory", target)
		}
		if !isSkillDir(target) {
			return "", 0, engine.WithCode(engine.ExitRefused, fmt.Errorf("%s exists and is not the pravo skill: move it, or install to another directory", target))
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	stage := target + ".new"
	os.RemoveAll(stage)
	n := 0
	err := fs.WalkDir(skills.FS, skills.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(skills.FS, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(stage, filepath.FromSlash(strings.TrimPrefix(p, skills.Dir+"/")))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		n++
		return os.WriteFile(dst, data, 0o644)
	})
	if err == nil {
		err = os.WriteFile(filepath.Join(stage, "VERSION"), []byte(Version+"\n"), 0o644)
		n++
	}
	if err != nil {
		os.RemoveAll(stage)
		return "", 0, err
	}
	if err := os.RemoveAll(target); err != nil {
		os.RemoveAll(stage)
		return "", 0, err
	}
	if err := os.Rename(stage, target); err != nil {
		os.RemoveAll(stage)
		return "", 0, err
	}
	return target, n, nil
}

// isSkillDir reports whether a directory holds the pravo skill: a SKILL.md
// whose name is pravo. An empty directory counts, since nothing is lost in it.
func isSkillDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	if len(entries) == 0 {
		return true
	}
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return false
	}
	return strings.HasPrefix(string(data), "---\nname: pravo\n")
}

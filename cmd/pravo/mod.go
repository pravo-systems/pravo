package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	cuecmd "cuelang.org/go/cmd/cue/cmd"
	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/ui"
)

// pravo mod: sharing modules. CUE's own module commands (init, tidy, get,
// edit, ...) are embedded and run with pravo's default registry mapping
// (github.com/<owner> modules live on ghcr.io/<owner>/cue); publish adds
// pre-flight checks; list and verify report what a root loads.

func modCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mod",
		Short: "Create, fetch and publish CUE modules for sharing",
		Long: `Sharing modules. Subcommands:

  pravo mod init <module-path@vN>   create cue.mod/module.cue
  pravo mod get <path@version>...   add or upgrade a dependency
  pravo mod tidy                    resolve and pin dependencies
  pravo mod publish <version>       publish this module (checks first)
  pravo mod list                    modules this root loads, with versions and hashes
  pravo mod verify                  compare loaded modules with pravo.lock.cue

All other CUE module commands (edit, mirror, ...) are passed through to CUE.
Modules under github.com/<owner> are fetched from, and published to,
ghcr.io/<owner>/cue unless CUE_REGISTRY says otherwise.`,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			args = extractGlobals(args)
			if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
				return cmd.Help()
			}
			switch args[0] {
			case "publish":
				return modPublish(cmd.Context(), args[1:])
			case "list":
				return modList()
			case "verify":
				return modVerify()
			}
			return runCueMod(cmd.Context(), args)
		},
	}
	return cmd
}

// extractGlobals takes pravo's own global flags out of args (the mod command
// passes everything else to CUE untouched) and applies them.
func extractGlobals(args []string) []string {
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json" || a == "-json":
			flagJSON = true
		case a == "--no-color" || a == "-no-color":
			flagNoColor = true
		case (a == "--actor" || a == "-actor") && i+1 < len(args):
			flagActor = args[i+1]
			i++
		case (a == "--reason" || a == "-reason") && i+1 < len(args):
			flagReason = args[i+1]
			i++
		case (a == "-C" || a == "--chdir") && i+1 < len(args):
			_ = os.Chdir(args[i+1])
			i++
		default:
			rest = append(rest, a)
		}
	}
	if flagJSON {
		out = ui.NewPrinter(os.Stderr, true)
	} else {
		out = ui.NewPrinter(os.Stdout, colourOff(flagNoColor))
	}
	return rest
}

// runCueMod runs "cue mod <args>" in-process with the default registry set.
func runCueMod(ctx context.Context, args []string) error {
	var extra []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if strings.Contains(a, "/") && strings.Contains(a, ".") {
			extra = append(extra, a)
		}
	}
	wd, _ := os.Getwd()
	// The in-process cue command reads the environment of the process.
	if reg := config.RegistryEnv(environment(), wd, extra...).Get("CUE_REGISTRY"); reg != "" {
		os.Setenv("CUE_REGISTRY", reg)
	}
	c, err := cuecmd.New(append([]string{"mod"}, args...))
	if err != nil {
		return err
	}
	if err := c.Run(ctx); err != nil {
		if errors.Is(err, cuecmd.ErrPrintedError) {
			return engine.WithCode(engine.ExitError, errSilent)
		}
		return err
	}
	return nil
}

func gitOutput(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// modPublish publishes the module in the current directory.
func modPublish(ctx context.Context, args []string) error {
	if len(args) != 1 || !strings.HasPrefix(args[0], "v") {
		return errors.New("usage: pravo mod publish <version>, for example v1.2.0")
	}
	version := args[0]
	wd, _ := os.Getwd()
	root := config.FindModuleRoot(wd)
	if root == "" {
		return errors.New("no cue.mod found: run \"pravo mod init <github.com/owner/name@v0>\" first")
	}
	r := &config.Root{Dir: root, ModRoot: root}
	mf, err := r.ModuleFile()
	if err != nil {
		return err
	}
	if mf.Module == "" || !strings.Contains(mf.Module, "@") {
		return fmt.Errorf("the module needs a path with a major version, such as github.com/owner/name@v0 (now %q)", mf.Module)
	}
	path := strings.SplitN(mf.Module, "@", 2)[0]
	if !strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
		return fmt.Errorf("module path %q: the first path element needs a dot (github.com/owner/name)", path)
	}
	if mf.Source == nil || mf.Source.Kind == "" {
		return errors.New("cue.mod/module.cue needs a source: run \"pravo mod edit --source git\"")
	}
	if mf.Source.Kind == "git" {
		if _, err := gitOutput(root, "rev-parse", "--git-dir"); err != nil {
			return fmt.Errorf("source is git but %s is not a git repository with a commit", root)
		}
		if st, _ := gitOutput(root, "status", "--porcelain"); st != "" {
			return fmt.Errorf("the working tree has uncommitted changes; a published version must match a commit:\n%s", st)
		}
	}
	if len(mf.Deps) > 0 {
		out.Printf("%s %d dependencies pinned in cue.mod/module.cue\n", out.C(ui.Dim, "Checked:"), len(mf.Deps))
	}
	if err := runCueMod(ctx, []string{"publish", version}); err != nil {
		return err
	}
	if owner, name, ok := ghcrLocation(path); ok && strings.Contains(os.Getenv("CUE_REGISTRY"), "ghcr.io/"+owner) {
		pkg := url.PathEscape(config.RegistryPrefix + "/" + path)
		out.Println()
		out.Printf("Stored as ghcr.io/%s/%s/%s:%s\n", owner, config.RegistryPrefix, path, version)
		out.Printf("A first push is private. To let others use %s without credentials, open\n", name)
		out.Printf("  https://github.com/users/%s/packages/container/package/%s\n", owner, strings.ReplaceAll(pkg, "/", "%2F"))
		out.Println("-> Package settings -> Danger Zone -> Change visibility -> Public. (GitHub offers no API for this; it can take minutes to show.)")
	}
	return nil
}

// ghcrLocation reports the ghcr.io owner and module name for a module under
// github.com/<owner>/.
func ghcrLocation(path string) (owner, name string, ok bool) {
	parts := strings.Split(path, "/")
	if len(parts) >= 3 && parts[0] == "github.com" {
		return parts[1], strings.Join(parts[2:], "/"), true
	}
	return "", "", false
}

func modList() error {
	root, err := loadRoot()
	if err != nil {
		return err
	}
	if flagJSON {
		list := []map[string]string{}
		for _, m := range root.Modules {
			list = append(list, map[string]string{"module": m.Path, "version": m.Version, "hash": m.Hash})
		}
		return emitJSON(list)
	}
	if len(root.Modules) == 0 {
		out.Println("No dependency modules are loaded.")
	}
	for _, m := range root.Modules {
		out.Printf("%s  %s  %s\n", out.C(ui.Bold, m.Path), m.Version, out.C(ui.Dim, m.Hash))
	}
	if root.SharedModule() {
		rel, _ := filepath.Rel(root.Dir, root.ModRoot)
		out.Printf("%s the module root is %s; packages in it are part of the specification and are covered by saved-plan fingerprints.\n", out.C(ui.Dim, "Note:"), rel)
	}
	return nil
}

func modVerify() error {
	root, err := loadRoot()
	if err != nil {
		return err
	}
	lock, err := engine.ReadLock(root.Dir)
	if err != nil {
		return err
	}
	var problems []string
	have := map[string]bool{}
	for _, m := range root.Modules {
		have[m.Path] = true
		e, ok := lock.Modules[m.Path]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s %s is not in %s (run pravo init)", m.Path, m.Version, engine.LockFileName))
		case e.Version != m.Version:
			problems = append(problems, fmt.Sprintf("%s: locked %s, loaded %s (run pravo init)", m.Path, e.Version, m.Version))
		case e.Hash != m.Hash:
			problems = append(problems, fmt.Sprintf("%s %s: content differs from the lock (%s, now %s)", m.Path, m.Version, e.Hash, m.Hash))
		}
	}
	for p := range lock.Modules {
		if !have[p] {
			problems = append(problems, p+" is locked but no longer used")
		}
	}
	sort.Strings(problems)
	if flagJSON {
		if problems == nil {
			problems = []string{}
		}
		if err := emitJSON(map[string]any{"ok": len(problems) == 0, "problems": problems}); err != nil {
			return err
		}
		if len(problems) > 0 {
			return engine.WithCode(engine.ExitRefused, errSilent)
		}
		return nil
	}
	if len(problems) == 0 {
		out.Println(out.C(ui.Bold+ui.Green, "Verified.") + fmt.Sprintf(" %d modules match %s.", len(root.Modules), engine.LockFileName))
		return nil
	}
	for _, p := range problems {
		out.Printf("  - %s\n", p)
	}
	return engine.WithCode(engine.ExitRefused, fmt.Errorf("%d module(s) do not match the lock", len(problems)))
}

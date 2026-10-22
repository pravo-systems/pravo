package main

import (
	"testing"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
)

func TestSetCommandKeepsTheLineAsTypedInAnUnsealedState(t *testing.T) {
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"apply", "-t", "env=prod", "--auto-approve", "plan-2f9c1a4e"}
	for name, st := range map[string]*state.Store{"a state": st, "no state": nil} {
		var opts engine.Options
		setCommand(st, args, &opts)
		if want := "pravo apply -t env=prod --auto-approve plan-2f9c1a4e"; opts.Command != want {
			t.Errorf("%s: command %q, want %q", name, opts.Command, want)
		}
		if len(opts.Args) != len(args) || opts.Args[2] != "env=prod" {
			t.Errorf("%s: args %q", name, opts.Args)
		}
	}
}

// commandFor finds the command that args runs in the real command tree and makes
// it the running one, with its flags parsed as a run would.
func commandFor(t *testing.T, args ...string) {
	t.Helper()
	root := newRootCmd()
	cmd, rest, err := root.Find(args)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.ParseFlags(rest); err != nil {
		t.Fatal(err)
	}
	was := runningCommand
	runningCommand = cmd
	t.Cleanup(func() { runningCommand = was })
}

func TestTheCommandTreeSaysWhichFlagsTakeAValue(t *testing.T) {
	commandFor(t, "apply", "--auto-approve")
	for _, c := range []struct {
		name  string
		short bool
		long  string
		takes bool
		known bool
	}{
		{"auto-approve", false, "auto-approve", false, true},
		{"actor", false, "actor", true, true},
		{"reason", false, "reason", true, true},
		{"chdir", false, "chdir", true, true},
		{"C", true, "chdir", true, true},
		{"t", true, "tag", true, true},
		{"T", true, "inject-vars", false, true},
		{"v", true, "verbose", false, true},
		{"json", false, "json", false, true},
		{"nonsense", false, "", false, false},
		{"Z", true, "", false, false},
		{"ab", true, "", false, false},
	} {
		k, ok := commandFlag(c.name, c.short)
		if ok != c.known || k.TakesValue != c.takes || k.Long != c.long {
			t.Errorf("commandFlag(%q, %v) = %+v, %v; want %s, takes %v, known %v", c.name, c.short, k, ok, c.long, c.takes, c.known)
		}
	}
	runningCommand = nil
	if _, ok := commandFlag("actor", false); ok {
		t.Error("no command is running and a flag is known")
	}
}

// The real flags of the commands, in the shapes that people type them.
func TestARealCommandLineSealsTheValuesOfItsValueFlags(t *testing.T) {
	tag := func(v string) string { return "T" }
	tok := "<sealed:T>"
	for _, c := range []struct {
		name string
		cmd  []string // the command the line runs
		argv []string
		want string
	}{
		{"a persistent flag before the command", []string{"plan"}, []string{"--actor", "ada", "--reason", "fix the db", "plan", "--store", "-t", "k=v"},
			"pravo --actor " + tok + " --reason " + tok + " plan --store -t k=" + tok},
		{"a boolean flag before a positional argument", []string{"apply"}, []string{"apply", "--auto-approve", "plan-2f9c1a4e", "--actor", "ada"},
			"pravo apply --auto-approve plan-2f9c1a4e --actor " + tok},
		{"the working directory stays", []string{"plan"}, []string{"-C", "/srv/w", "--actor", "ada", "plan"}, "pravo -C /srv/w --actor " + tok + " plan"},
		{"the tags in every spelling", []string{"plan"}, []string{"plan", "-tkey=v", "--tag", "a=b", "-T", "-t", "c=d"},
			"pravo plan -tkey=" + tok + " --tag a=" + tok + " -T -t c=" + tok},
		{"a public flag", []string{"plan"}, []string{"plan", "--out", "x.plan", "--actor=ada"}, "pravo plan --out x.plan --actor=" + tok},
		{"the double dash", []string{"state", "mv"}, []string{"state", "mv", "--actor", "ada", "--", "--actor", "b"}, "pravo state mv --actor " + tok + " -- --actor b"},
	} {
		t.Run(c.name, func(t *testing.T) {
			commandFor(t, c.cmd...)
			if got := state.RedactArgs(c.argv, tag); got != c.want {
				t.Errorf("RedactArgs(%q)\n got %s\nwant %s", c.argv, got, c.want)
			}
		})
	}
}

package main

import "github.com/pravo-systems/pravo/internal/ui"

// rootFlags holds the values of the flags of the command that runs. The root
// command binds the global ones; the commands that read the readers file bind
// AccessDir. They are one value, so that a caller that runs a command in
// process can save it, set its own and put it back.
type rootFlags struct {
	Dir     string // -C: run as if started in this directory
	NoColor bool
	Verbose int
	// Tags and SysTags select the tags of the specification (-t, -T).
	Tags    []string
	SysTags bool
	// Env is --env NAME, which the root command folds into Tags as env=NAME.
	Env  string
	JSON bool
	// Markdown is the --markdown flag of plan and show: the plan as a comment.
	Markdown bool
	// Withhold is --withhold-sensitive: the JSON plan document carries {"$sensitive": true} in place of
	// the sensitive and write-only attributes and the sensitive outputs.
	Withhold bool
	Actor    string
	Reason   string
	// Identity is the --identity flag: the name of a file in the identity
	// directory, for every command that opens a sealed state. It takes
	// precedence over $PRAVO_IDENTITY.
	Identity string
	// ProviderMirror is the --provider-mirror flag, repeatable: it sets PRAVO_PROVIDER_MIRROR
	// for the run, so that providers are downloaded from a mirror.
	ProviderMirror []string
	// AccessDir is the --access-dir flag of the commands that read the readers
	// file.
	AccessDir string
}

// runContext is what a command that runs works with: its flags and the printer of its output.
// It is made once for a run and handed to every function that needs it.
type runContext struct {
	flags rootFlags
	out   *ui.Printer
}

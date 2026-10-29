package main

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
	JSON    bool
	// Markdown is the --markdown flag of plan and show: the plan as a comment.
	Markdown bool
	Actor    string
	Reason   string
	// Identity is the --identity flag: the name of a file in the identity
	// directory, for every command that opens a sealed state. It takes
	// precedence over $PRAVO_IDENTITY.
	Identity string
	// AccessDir is the --access-dir flag of the commands that read the readers
	// file.
	AccessDir string
}

var flags rootFlags

package plugin

import "os/exec"

// A provider is a child process that holds the environment of the run (cloud
// keys, tokens). When pravo is killed without a chance to clean up (kill -9, the
// OOM killer, a CI runner that gives up on a job), go-plugin never gets to
// kill it, so the provider would run on for good under init. These hooks tie
// the life of the provider to the life of pravo: guardChild is called before the
// provider starts, watchChild after, and the func it returns is called when the
// provider is closed normally.
//
//	Linux:    PR_SET_PDEATHSIG, the kernel kills the provider when pravo dies.
//	Windows:  a job object with KILL_ON_JOB_CLOSE, the system kills the provider
//	          when the last handle to the job (held by pravo) closes.
//	macOS/BSD: no kernel facility, so a small shell watcher holds the read end of
//	          a pipe whose write end only pravo has; when pravo dies the pipe
//	          closes and the watcher kills the provider.
func guardChild(cmd *exec.Cmd) { guardChildOS(cmd) }

// watchChild is called once cmd has started. The returned func stops the
// watching; it is safe to call more than once.
func watchChild(cmd *exec.Cmd) func() { return watchChildOS(cmd) }

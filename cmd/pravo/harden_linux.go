//go:build linux

package main

import "golang.org/x/sys/unix"

// hardenProcess makes pravo a process that another process of the same user cannot
// read: not its memory, and not /proc/PID/environ, which holds the passphrase and
// the identity that the environment of a provider is scrubbed of. A provider is
// pravo's child and runs as the same user, so without this it reads them from its
// parent. A process that runs as root, or that can become root, is not stopped by
// it; a provider that must be held back from those needs more than this.
func hardenProcess() {
	_ = unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
}

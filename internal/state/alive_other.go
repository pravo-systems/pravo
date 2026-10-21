//go:build !unix && !windows

package state

// processAlive cannot tell on this system, so a lock holder is taken to be gone
// only when its lock goes stale.
func processAlive(pid int) bool { return pid > 0 }

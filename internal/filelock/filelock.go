// Package filelock takes an exclusive advisory lock on an open file, which the
// system releases when the file is closed or the process ends. A filesystem
// that has no file locks gets none: Lock reports the error, and the callers
// that can do without a lock ignore it.
package filelock

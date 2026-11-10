//go:build !linux

package main

// hardenProcess does nothing where there is no PR_SET_DUMPABLE.
func hardenProcess() {}

//go:build unix

package main

import (
	"os"
	"runtime"
	"syscall"
)

// peakRSS is the peak resident set size of the process that ended, in
// megabytes, or 0 when the system does not report it.
func peakRSS(ps *os.ProcessState) float64 {
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0
	}
	rss := float64(ru.Maxrss) / 1024
	if runtime.GOOS == "darwin" {
		rss /= 1024 // Darwin reports bytes, other systems kilobytes
	}
	return rss
}

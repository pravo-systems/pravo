//go:build !unix

package main

import "os"

// peakRSS is not reported on this system.
func peakRSS(*os.ProcessState) float64 { return 0 }

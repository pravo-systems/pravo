//go:build linux

package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// A child of pravo, which a provider is, cannot read the environment of pravo.
func TestHardenProcessHidesTheEnvironmentFromAChild(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every process")
	}
	hardenProcess()
	if got, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0); err != nil || got != 0 {
		t.Fatalf("dumpable is %d, %v", got, err)
	}
	cmd := exec.Command("cat", "/proc/"+strconv.Itoa(os.Getpid())+"/environ")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "Permission denied") {
		t.Errorf("a child read the environment of pravo: %v\n%s", err, out)
	}
}

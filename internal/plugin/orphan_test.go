//go:build !windows

package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain doubles as the "pravo" of these tests: with ORPHAN_HELPER set the
// test binary starts a sleeping child guarded the way a provider is, prints its
// pid and waits to be killed.
func TestMain(m *testing.M) {
	if mode := os.Getenv("ORPHAN_HELPER"); mode != "" {
		// The child starts a child of its own, as a provider that runs a plugin does.
		child := exec.Command("sh", "-c", "sleep 300 & wait")
		var stop func()
		if mode == "shell" {
			child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			child.Start()
			stop = watchWithShell(child)
		} else {
			guardChild(child)
			child.Start()
			stop = watchChild(child)
		}
		_ = stop
		time.Sleep(300 * time.Millisecond) // let sh start its sleep
		fmt.Println(child.Process.Pid)
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func alive(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	// A zombie that nobody reaped still answers signal 0.
	b, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	return !strings.Contains(string(b), ") Z ")
}

func testOrphan(t *testing.T, mode string) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "ORPHAN_HELPER="+mode)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err := fmt.Fscan(out, &pid); err != nil {
		t.Fatal(err)
	}
	pids := []int{pid}
	if b, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output(); err == nil {
		for _, f := range strings.Fields(string(b)) {
			n, _ := strconv.Atoi(f)
			pids = append(pids, n)
		}
	}
	t.Cleanup(func() {
		for _, p := range pids {
			syscall.Kill(p, syscall.SIGKILL)
		}
	})
	if len(pids) != 2 || !alive(pid) {
		t.Fatalf("expected the child and its own child before the kill, got %v", pids)
	}
	cmd.Process.Kill()
	cmd.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for _, p := range pids {
		for alive(p) {
			if time.Now().After(deadline) {
				t.Fatalf("process %d (of %v) still running 5s after its parent was killed", p, pids)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestChildDiesWithParent(t *testing.T) { testOrphan(t, "default") }
func TestChildDiesWithParentShellWatcher(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("needs /proc")
	}
	testOrphan(t, "shell")
}

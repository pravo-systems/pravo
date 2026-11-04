//go:build windows

package plugin

import (
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	jobOnce sync.Once
	job     windows.Handle
)

// providerJob is one job object for all providers of this process. Its handle is
// never closed: the system closes it when pravo ends, however it ends, and the
// limit then kills every process still in the job.
func providerJob() windows.Handle {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			_ = windows.CloseHandle(h)
			return
		}
		job = h
	})
	return job
}

func guardChildOS(*exec.Cmd) {}

func killGroupOS(*exec.Cmd) {}

func watchChildOS(cmd *exec.Cmd) func() {
	j := providerJob()
	if j == 0 || cmd.Process == nil {
		return func() {}
	}
	const access = windows.PROCESS_SET_QUOTA | windows.PROCESS_TERMINATE
	ph, err := windows.OpenProcess(access, false, uint32(cmd.Process.Pid))
	if err != nil {
		return func() {}
	}
	defer func() { _ = windows.CloseHandle(ph) }()
	_ = windows.AssignProcessToJobObject(j, ph)
	return func() {}
}

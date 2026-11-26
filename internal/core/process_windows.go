package core

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const maxLogBytes = 2 * 1024 * 1024

type running struct {
	cmd      *exec.Cmd
	job      windows.Handle
	logFile  *os.File
	stopLog  chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

func newJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func assignPID(job windows.Handle, pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(job, h)
}

func commandHidden(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	return cmd
}

func startChild(binary, workDir, logPath string, args []string) (*running, error) {
	logf, err := openLog(logPath)
	if err != nil {
		return nil, err
	}
	job, err := newJob()
	if err != nil {
		_ = logf.Close()
		return nil, err
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = workDir
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		_ = logf.Close()
		return nil, err
	}
	if err := assignPID(job, cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = windows.CloseHandle(job)
		_ = logf.Close()
		return nil, err
	}
	r := &running{
		cmd:     cmd,
		job:     job,
		logFile: logf,
		stopLog: make(chan struct{}),
		done:    make(chan struct{}),
	}
	go r.watchLog(logPath)
	go func() {
		_ = cmd.Wait()
		close(r.done)
	}()
	return r, nil
}

func (r *running) watchLog(path string) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.stopLog:
			return
		case <-tick.C:
			_ = trimLogIfLarge(path, maxLogBytes)
		}
	}
}

func (r *running) stopMonitor() {
	if r == nil || r.stopLog == nil {
		return
	}
	r.stopOnce.Do(func() { close(r.stopLog) })
}

func cleanupRun(r *running) {
	if r == nil {
		return
	}
	r.stopMonitor()
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
	if r.job != 0 {
		_ = windows.CloseHandle(r.job)
		r.job = 0
	}
	if r.done != nil {
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
		}
	}
	if r.logFile != nil {
		_ = r.logFile.Close()
		r.logFile = nil
	}
}

func openLog(path string) (*os.File, error) {
	if err := trimLogIfLarge(path, maxLogBytes); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
}

func trimLogIfLarge(path string, max int64) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Size() <= max {
		return nil
	}
	return os.Truncate(path, 0)
}

func visibleWindowCount(pid int) int {
	var n int
	cb := windows.NewCallback(func(hwnd windows.HWND, lparam uintptr) uintptr {
		var owner uint32
		_, _ = windows.GetWindowThreadProcessId(hwnd, &owner)
		if int(owner) == pid && windows.IsWindowVisible(hwnd) {
			n++
		}
		return 1
	})
	_ = windows.EnumWindows(cb, nil)
	return n
}

func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259
}

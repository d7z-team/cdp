//go:build !windows

package launcher

import (
	"os"
	"syscall"
)

const connectionRefusedError = syscall.ECONNREFUSED

func browserAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setsid: true,
	}
}

func killBrowserProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	if err := syscall.Kill(-process.Pid, syscall.SIGKILL); err != nil {
		return process.Kill()
	}
	return nil
}

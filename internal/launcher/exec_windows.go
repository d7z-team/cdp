//go:build windows

package launcher

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

const connectionRefusedError = windows.WSAECONNREFUSED

func browserAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow: true,
	}
}

func killBrowserProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	return process.Kill()
}

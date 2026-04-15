//go:build windows

package daemon

import (
	"os/exec"
	"syscall"
)

// detach configures cmd so its child process is fully detached from the
// parent console on Windows.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
}

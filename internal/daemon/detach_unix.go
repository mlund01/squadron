//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

// detach configures cmd so its child process becomes the leader of a new
// session, fully detaching from the controlling terminal.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

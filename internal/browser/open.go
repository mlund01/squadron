// Package browser opens a URL in the user's default browser. It exists so
// squadron doesn't have multiple copies of the OS-dispatch logic for the
// handful of callers that need to launch a browser (the serve command's
// command-center launcher, and the mcp OAuth login flow's loopback
// callback handler).
package browser

import (
	"os/exec"
	"runtime"
)

// Open launches the platform default browser pointed at url. Errors from
// launching the child process are ignored — every caller falls back to
// printing the URL so the user can open it manually if the launcher fails.
func Open(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return
	}
	_ = cmd.Start()
}

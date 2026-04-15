package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// PidFilePath returns the path to the PID file for a given config directory.
func PidFilePath(configPath string) string {
	return filepath.Join(resolveConfigDir(configPath), ".squadron", "engage.pid")
}

// ReadyFilePath returns the path to the ready signal file for a given config directory.
func ReadyFilePath(configPath string) string {
	return filepath.Join(resolveConfigDir(configPath), ".squadron", "engage.ready")
}

// Ready-file protocol. The child writes one of:
//
//	readyOK             — success, no command center launched
//	readyOK + ":<port>" — success, command center bound to <port>
//	readyErrPrefix + msg — failure; msg is shown to the user
const (
	readyOK        = "ok"
	readyErrPrefix = "error: "
)

// SignalReady writes a success marker to the ready file. Pass ccPort=0 if no
// command center was launched.
func SignalReady(configPath string, ccPort int) {
	content := readyOK
	if ccPort > 0 {
		content = fmt.Sprintf("%s:%d", readyOK, ccPort)
	}
	os.WriteFile(ReadyFilePath(configPath), []byte(content), 0644)
}

// SignalFailed writes the error message to the ready file so the parent can report it.
func SignalFailed(configPath string, err error) {
	os.WriteFile(ReadyFilePath(configPath), []byte(readyErrPrefix+err.Error()), 0644)
}

// ClearReady removes the ready file (called on startup before config loads).
func ClearReady(configPath string) {
	os.Remove(ReadyFilePath(configPath))
}

// CleanupFailedFork removes the PID and ready files for a fork where the child
// signaled failure (and has already exited on its own).
func CleanupFailedFork(configPath string) {
	os.Remove(PidFilePath(configPath))
	os.Remove(ReadyFilePath(configPath))
}

// ReadyResult reports what the child signaled back to the parent.
type ReadyResult struct {
	OK     bool
	Error  string
	CCPort int // command center port if the child launched one, else 0
}

// WaitReady polls for the ready file. On success returns OK=true with the
// command center port (0 if none was launched). On failure returns OK=false
// with the error message.
// minWait ensures the caller's UI (e.g. spinner) displays for at least
// a minimum duration even if the child signals instantly.
func WaitReady(configPath string, timeout, minWait time.Duration) ReadyResult {
	path := ReadyFilePath(configPath)
	deadline := time.Now().Add(timeout)
	earliest := time.Now().Add(minWait)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			content := strings.TrimSpace(string(data))
			if rest, ok := strings.CutPrefix(content, readyErrPrefix); ok {
				return ReadyResult{OK: false, Error: rest}
			}
			if content == readyOK || strings.HasPrefix(content, readyOK+":") {
				if wait := time.Until(earliest); wait > 0 {
					time.Sleep(wait)
				}
				port := 0
				if rest, ok := strings.CutPrefix(content, readyOK+":"); ok {
					port, _ = strconv.Atoi(rest)
				}
				return ReadyResult{OK: true, CCPort: port}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return ReadyResult{OK: false, Error: "timed out waiting for config to load"}
}

// LogFilePath returns the path to the log file for a given config directory.
func LogFilePath(configPath string) string {
	return filepath.Join(resolveConfigDir(configPath), ".squadron", "engage.log")
}

// Fork re-executes the current binary with --foreground in the background.
// It writes the child PID to .squadron/engage.pid and returns the PID.
func Fork(configPath string, extraFlags []string) (int, error) {
	self, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("could not determine executable path: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return 0, fmt.Errorf("could not resolve executable path: %w", err)
	}

	absConfig, err := filepath.Abs(configPath)
	if err != nil {
		return 0, fmt.Errorf("could not resolve config path: %w", err)
	}

	// Check if already running
	if running, pid := IsRunning(configPath); running {
		return 0, fmt.Errorf("squadron is already running (PID %d)", pid)
	}

	// Ensure .squadron directory exists
	sqDir := filepath.Join(resolveConfigDir(absConfig), ".squadron")
	if err := os.MkdirAll(sqDir, 0755); err != nil {
		return 0, fmt.Errorf("could not create .squadron directory: %w", err)
	}

	// Open log file
	logPath := LogFilePath(absConfig)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return 0, fmt.Errorf("could not open log file: %w", err)
	}

	// Build command: squadron engage --foreground -c <absConfigPath> <extraFlags...>
	args := []string{"engage", "--foreground", "-c", absConfig}
	args = append(args, extraFlags...)

	cmd := exec.Command(self, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Dir = resolveConfigDir(absConfig)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return 0, fmt.Errorf("failed to start background process: %w", err)
	}
	logFile.Close()

	pid := cmd.Process.Pid

	// Write PID file
	pidPath := PidFilePath(absConfig)
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0644); err != nil {
		// Kill the process we just started since we can't track it
		cmd.Process.Kill()
		return 0, fmt.Errorf("could not write PID file: %w", err)
	}

	// Wait briefly to ensure process started successfully
	time.Sleep(500 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		os.Remove(pidPath)
		return 0, fmt.Errorf("background process exited immediately — check %s for details", logPath)
	}

	// Release the process so it's not tied to this parent
	cmd.Process.Release()

	return pid, nil
}

// Stop reads the PID file and gracefully stops the background process.
func Stop(configPath string) error {
	absConfig, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("could not resolve config path: %w", err)
	}

	pidPath := PidFilePath(absConfig)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return fmt.Errorf("no PID file found — squadron may not be running")
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		os.Remove(pidPath)
		return fmt.Errorf("invalid PID file")
	}

	// Send SIGTERM
	process, err := os.FindProcess(pid)
	if err != nil {
		os.Remove(pidPath)
		return fmt.Errorf("process %d not found", pid)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		// Process already dead
		os.Remove(pidPath)
		return nil
	}

	// Wait up to 10 seconds for graceful shutdown
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			// Process has exited
			os.Remove(pidPath)
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}

	// Force kill
	process.Signal(syscall.SIGKILL)
	os.Remove(pidPath)
	return nil
}

// IsRunning checks if a Squadron process is running for the given config path.
func IsRunning(configPath string) (bool, int) {
	absConfig, _ := filepath.Abs(configPath)
	pidPath := PidFilePath(absConfig)

	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false, 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false, 0
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false, 0
	}

	// Check if process is actually alive
	if err := process.Signal(syscall.Signal(0)); err != nil {
		// Stale PID file — clean up
		os.Remove(pidPath)
		return false, 0
	}

	return true, pid
}

// resolveConfigDir returns the directory component of a config path.
func resolveConfigDir(configPath string) string {
	info, err := os.Stat(configPath)
	if err != nil {
		return configPath
	}
	if info.IsDir() {
		return configPath
	}
	return filepath.Dir(configPath)
}

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPathHelpers(t *testing.T) {
	dir := t.TempDir()

	if got, want := PidFilePath(dir), filepath.Join(dir, ".squadron", "engage.pid"); got != want {
		t.Errorf("PidFilePath = %q, want %q", got, want)
	}
	if got, want := ReadyFilePath(dir), filepath.Join(dir, ".squadron", "engage.ready"); got != want {
		t.Errorf("ReadyFilePath = %q, want %q", got, want)
	}
	if got, want := LogFilePath(dir), filepath.Join(dir, ".squadron", "engage.log"); got != want {
		t.Errorf("LogFilePath = %q, want %q", got, want)
	}
}

func TestPathHelpers_ConfigPathIsFile(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "squadron.hcl")
	if err := os.WriteFile(cfgFile, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	// When configPath is a file, helpers should return paths relative to its directory.
	if got, want := PidFilePath(cfgFile), filepath.Join(dir, ".squadron", "engage.pid"); got != want {
		t.Errorf("PidFilePath(file) = %q, want %q", got, want)
	}
}

func TestSignalReadyAndWaitReady_Success(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".squadron"), 0755); err != nil {
		t.Fatal(err)
	}

	SignalReady(dir, 0)

	result := WaitReady(dir, 1*time.Second, 0)
	if !result.OK {
		t.Fatalf("WaitReady OK=false, err=%q", result.Error)
	}
	if result.CCPort != 0 {
		t.Errorf("CCPort = %d, want 0", result.CCPort)
	}
}

func TestSignalReadyAndWaitReady_WithPort(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".squadron"), 0755); err != nil {
		t.Fatal(err)
	}

	SignalReady(dir, 8081)

	result := WaitReady(dir, 1*time.Second, 0)
	if !result.OK {
		t.Fatalf("WaitReady OK=false, err=%q", result.Error)
	}
	if result.CCPort != 8081 {
		t.Errorf("CCPort = %d, want 8081", result.CCPort)
	}
}

func TestWaitReady_Failure(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".squadron"), 0755); err != nil {
		t.Fatal(err)
	}

	SignalFailed(dir, fmt.Errorf("boom"))

	result := WaitReady(dir, 1*time.Second, 0)
	if result.OK {
		t.Fatal("WaitReady OK=true, want false")
	}
	if result.Error != "boom" {
		t.Errorf("Error = %q, want %q", result.Error, "boom")
	}
}

func TestWaitReady_Timeout(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".squadron"), 0755); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	result := WaitReady(dir, 300*time.Millisecond, 0)
	elapsed := time.Since(start)

	if result.OK {
		t.Fatal("WaitReady OK=true, want false on timeout")
	}
	if elapsed < 250*time.Millisecond {
		t.Errorf("returned too early: %v", elapsed)
	}
	if result.Error == "" {
		t.Error("expected non-empty error on timeout")
	}
}

func TestWaitReady_MinWait(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".squadron"), 0755); err != nil {
		t.Fatal(err)
	}

	// Signal ready immediately — WaitReady should still wait at least minWait.
	SignalReady(dir, 0)

	start := time.Now()
	result := WaitReady(dir, 1*time.Second, 400*time.Millisecond)
	elapsed := time.Since(start)

	if !result.OK {
		t.Fatalf("WaitReady OK=false, err=%q", result.Error)
	}
	if elapsed < 400*time.Millisecond {
		t.Errorf("minWait not respected: elapsed=%v, want >=400ms", elapsed)
	}
}

func TestWaitReady_FailureIgnoresMinWait(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".squadron"), 0755); err != nil {
		t.Fatal(err)
	}

	SignalFailed(dir, fmt.Errorf("fast fail"))

	start := time.Now()
	result := WaitReady(dir, 2*time.Second, 5*time.Second)
	elapsed := time.Since(start)

	if result.OK {
		t.Fatal("WaitReady OK=true, want false")
	}
	// Failures should return well before minWait (5s) — certainly well below 1s.
	if elapsed > 1*time.Second {
		t.Errorf("failure waited too long: %v (expected immediate)", elapsed)
	}
}

func TestClearReady(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".squadron"), 0755); err != nil {
		t.Fatal(err)
	}

	SignalReady(dir, 0)
	if _, err := os.Stat(ReadyFilePath(dir)); err != nil {
		t.Fatalf("ready file not created: %v", err)
	}

	ClearReady(dir)
	if _, err := os.Stat(ReadyFilePath(dir)); !os.IsNotExist(err) {
		t.Errorf("ready file still exists after ClearReady")
	}
}

func TestIsRunning_NoPidFile(t *testing.T) {
	dir := t.TempDir()
	running, pid := IsRunning(dir)
	if running {
		t.Error("IsRunning true with no PID file")
	}
	if pid != 0 {
		t.Errorf("pid = %d, want 0", pid)
	}
}

func TestIsRunning_StalePidFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".squadron"), 0755); err != nil {
		t.Fatal(err)
	}

	// Write a PID that almost certainly isn't ours. Use a very high PID to avoid
	// accidentally matching a real process.
	if err := os.WriteFile(PidFilePath(dir), []byte("999999"), 0644); err != nil {
		t.Fatal(err)
	}

	running, _ := IsRunning(dir)
	if running {
		t.Error("IsRunning true for a nonexistent process")
	}
	// Stale PID file should be cleaned up.
	if _, err := os.Stat(PidFilePath(dir)); !os.IsNotExist(err) {
		t.Errorf("stale PID file not cleaned up")
	}
}

func TestIsRunning_LiveProcess(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".squadron"), 0755); err != nil {
		t.Fatal(err)
	}

	// The current test process is definitely alive.
	pid := os.Getpid()
	if err := os.WriteFile(PidFilePath(dir), []byte(fmt.Sprintf("%d", pid)), 0644); err != nil {
		t.Fatal(err)
	}

	running, got := IsRunning(dir)
	if !running {
		t.Error("IsRunning false for a live process")
	}
	if got != pid {
		t.Errorf("pid = %d, want %d", got, pid)
	}
}

func TestResolveConfigDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "squadron.hcl")
	if err := os.WriteFile(file, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	if got := resolveConfigDir(dir); got != dir {
		t.Errorf("resolveConfigDir(dir) = %q, want %q", got, dir)
	}
	if got := resolveConfigDir(file); got != dir {
		t.Errorf("resolveConfigDir(file) = %q, want %q", got, dir)
	}
}

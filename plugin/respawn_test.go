package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupChaosPlugin(t *testing.T) {
	home := os.Getenv("CHAOS_SQUADRON_HOME")
	if home == "" {
		t.Skip("set CHAOS_SQUADRON_HOME to a SQUADRON_HOME with chaos_plugin/local installed (see testground/chaos_plugin/)")
	}
	t.Setenv("SQUADRON_HOME", home)
	dir, err := GetPluginDir("chaos_plugin", "local")
	if err != nil {
		t.Fatalf("get plugin dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, runnerFileName)); err != nil {
		t.Skipf("chaos_plugin not installed at %s — run `squadron plugin build chaos_plugin <chaos_plugin source>` first", dir)
	}
}

func TestPluginRespawnsAfterCrash(t *testing.T) {
	setupChaosPlugin(t)
	defer CloseAll()

	pc, err := LoadPlugin("chaos_plugin", "local", "")
	if err != nil {
		t.Fatalf("first load: %v", err)
	}

	first, err := pc.Call(context.Background(), "status", "{}")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	var firstStatus struct {
		Pid       int     `json:"pid"`
		Age       float64 `json:"age_seconds"`
		CallCount int     `json:"call_count"`
	}
	if err := json.Unmarshal([]byte(first), &firstStatus); err != nil {
		t.Fatalf("decode first status: %v (raw: %s)", err, first)
	}
	t.Logf("before crash: pid=%d age=%.2f calls=%d", firstStatus.Pid, firstStatus.Age, firstStatus.CallCount)
	if firstStatus.CallCount != 1 {
		t.Fatalf("expected first call_count=1, got %d", firstStatus.CallCount)
	}

	t.Log("waiting 6s for plugin to self-destruct...")
	time.Sleep(6 * time.Second)

	pc2, err := LoadPlugin("chaos_plugin", "local", "")
	if err != nil {
		t.Fatalf("second load (post-crash): %v", err)
	}

	second, err := pc2.Call(context.Background(), "status", "{}")
	if err != nil {
		t.Fatalf("second call (post-crash): %v", err)
	}

	var secondStatus struct {
		Pid       int     `json:"pid"`
		Age       float64 `json:"age_seconds"`
		CallCount int     `json:"call_count"`
	}
	if err := json.Unmarshal([]byte(second), &secondStatus); err != nil {
		t.Fatalf("decode second status: %v (raw: %s)", err, second)
	}
	t.Logf("after crash:  pid=%d age=%.2f calls=%d", secondStatus.Pid, secondStatus.Age, secondStatus.CallCount)

	if secondStatus.Pid == firstStatus.Pid {
		t.Fatalf("expected new pid after crash, both calls had pid=%d", secondStatus.Pid)
	}
	if secondStatus.CallCount != 1 {
		t.Errorf("expected fresh process to have call_count=1, got %d", secondStatus.CallCount)
	}
	if secondStatus.Age >= firstStatus.Age {
		t.Errorf("expected fresh process age (%.2fs) to be less than original (%.2fs)", secondStatus.Age, firstStatus.Age)
	}
}

func TestPluginRespawnsAfterExplicitCrash(t *testing.T) {
	setupChaosPlugin(t)
	defer CloseAll()

	pc, err := LoadPlugin("chaos_plugin", "local", "")
	if err != nil {
		t.Fatalf("first load: %v", err)
	}

	if _, err := pc.Call(context.Background(), "status", "{}"); err != nil {
		t.Fatalf("status call: %v", err)
	}

	if _, err := pc.Call(context.Background(), "crash", "{}"); err != nil {
		t.Fatalf("crash call: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	pc2, err := LoadPlugin("chaos_plugin", "local", "")
	if err != nil {
		t.Fatalf("reload after crash: %v", err)
	}

	if _, err := pc2.Call(context.Background(), "status", "{}"); err != nil {
		t.Fatalf("post-crash call: %v", err)
	}
}

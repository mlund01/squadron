package aitools_test

import (
	"context"
	"testing"
	"time"

	"squadron/aitools"
)

func TestSleepToolResumesFromPersistedStartTime(t *testing.T) {
	tool := &aitools.SleepTool{}
	ctx := aitools.WithInvocationMetadata(context.Background(), aitools.InvocationMetadata{
		StartedAt: time.Now().Add(-2 * time.Second),
	})
	started := time.Now()
	if result := tool.Call(ctx, `{"seconds":1}`); result != "Slept for 1.0 seconds." {
		t.Fatalf("Call() = %q", result)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("recovered sleep restarted its duration: %s", elapsed)
	}
	if !aitools.IsReplaySafe(tool) {
		t.Fatal("SleepTool must be replay-safe with persisted start time")
	}
}

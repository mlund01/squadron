package aitools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestTaskComplete_DefaultSuccess(t *testing.T) {
	tc := &TaskCompleteTool{}
	result := tc.Call(context.Background(), `{}`)

	if !tc.IsCompleted() {
		t.Fatal("expected IsCompleted() to be true")
	}
	if !tc.IsSucceeded() {
		t.Fatal("expected IsSucceeded() to be true for default call")
	}
	if tc.FailureReason() != "" {
		t.Fatalf("expected empty failure reason, got %q", tc.FailureReason())
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	if resp["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", resp["status"])
	}
}

func TestTaskComplete_ExplicitSuccess(t *testing.T) {
	tc := &TaskCompleteTool{}
	result := tc.Call(context.Background(), `{"succeed": true}`)

	if !tc.IsCompleted() {
		t.Fatal("expected IsCompleted() to be true")
	}
	if !tc.IsSucceeded() {
		t.Fatal("expected IsSucceeded() to be true")
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	if resp["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", resp["status"])
	}
}

func TestTaskComplete_FailureWithReason(t *testing.T) {
	tc := &TaskCompleteTool{}
	result := tc.Call(context.Background(), `{"succeed": false, "reason": "API rate limit exceeded"}`)

	if !tc.IsCompleted() {
		t.Fatal("expected IsCompleted() to be true")
	}
	if tc.IsSucceeded() {
		t.Fatal("expected IsSucceeded() to be false")
	}
	if tc.FailureReason() != "API rate limit exceeded" {
		t.Fatalf("expected reason 'API rate limit exceeded', got %q", tc.FailureReason())
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	if resp["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", resp["status"])
	}
}

func TestTaskComplete_FailureWithoutReasonRejected(t *testing.T) {
	tc := &TaskCompleteTool{}
	result := tc.Call(context.Background(), `{"succeed": false}`)

	if tc.IsCompleted() {
		t.Fatal("expected IsCompleted() to be false — missing reason should be rejected")
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	if resp["status"] != "error" {
		t.Fatalf("expected status error, got %v", resp["status"])
	}
}

func TestTaskComplete_SuccessIgnoresReason(t *testing.T) {
	tc := &TaskCompleteTool{}
	tc.Call(context.Background(), `{"succeed": true, "reason": "should be ignored"}`)

	if !tc.IsSucceeded() {
		t.Fatal("expected IsSucceeded() to be true")
	}
	if tc.FailureReason() != "" {
		t.Fatalf("expected empty failure reason on success, got %q", tc.FailureReason())
	}
}

func TestTaskComplete_SuccessBlockedByIncompleteSubtasks(t *testing.T) {
	tc := &TaskCompleteTool{}
	tc.SubtaskChecker = func() (int, int) { return 3, 2 }

	result := tc.Call(context.Background(), `{}`)

	if tc.IsCompleted() {
		t.Fatal("expected IsCompleted() to be false when subtasks incomplete")
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	if resp["status"] != "error" {
		t.Fatalf("expected status error, got %v", resp["status"])
	}
}

func TestTaskComplete_FailureSkipsSubtaskCheck(t *testing.T) {
	tc := &TaskCompleteTool{}
	tc.SubtaskChecker = func() (int, int) { return 3, 2 }

	result := tc.Call(context.Background(), `{"succeed": false, "reason": "cannot proceed"}`)

	if !tc.IsCompleted() {
		t.Fatal("expected IsCompleted() to be true — failure should skip subtask check")
	}
	if tc.IsSucceeded() {
		t.Fatal("expected IsSucceeded() to be false")
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	if resp["status"] != "ok" {
		t.Fatalf("expected status ok (failure accepted), got %v", resp["status"])
	}
}

func TestTaskComplete_SuccessWithAllSubtasksComplete(t *testing.T) {
	tc := &TaskCompleteTool{}
	tc.SubtaskChecker = func() (int, int) { return 3, 0 }

	tc.Call(context.Background(), `{}`)

	if !tc.IsCompleted() {
		t.Fatal("expected IsCompleted() to be true")
	}
	if !tc.IsSucceeded() {
		t.Fatal("expected IsSucceeded() to be true")
	}
}

func TestTaskComplete_EmptyParams(t *testing.T) {
	tc := &TaskCompleteTool{}
	tc.Call(context.Background(), ``)

	if !tc.IsCompleted() {
		t.Fatal("expected IsCompleted() to be true")
	}
	if !tc.IsSucceeded() {
		t.Fatal("expected IsSucceeded() to be true for empty params")
	}
}

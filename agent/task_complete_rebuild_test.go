package agent

import (
	"encoding/json"
	"testing"

	"squadron/aitools"
	"squadron/llm"
)

// rebuildTaskCompleteFromHistory must restore in-memory completion state
// from the session history when the previous run's kill landed between
// the tool_result persistence and the runner's UpdateTaskStatus call.
func TestRebuildTaskCompleteFromHistory_SuccessfulCompletion(t *testing.T) {
	c := &Commander{taskComplete: &aitools.TaskCompleteTool{}}

	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "do the thing"},
		{Role: llm.RoleAssistant, Parts: []llm.ContentBlock{
			{Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{
				ID:    "call_done",
				Name:  "task_complete",
				Input: json.RawMessage(`{"summary":"finished it","succeed":true}`),
			}},
		}},
		{Role: llm.RoleUser, Parts: []llm.ContentBlock{
			{Type: llm.ContentTypeToolResult, ToolResult: &llm.ToolResultBlock{
				ToolUseID: "call_done",
				Content:   `{"status":"ok"}`,
			}},
		}},
	}

	c.rebuildTaskCompleteFromHistory(msgs)

	if !c.taskComplete.IsCompleted() {
		t.Fatal("taskComplete.completed should be true after rebuild")
	}
	if !c.taskComplete.IsSucceeded() {
		t.Fatal("taskComplete.succeeded should be true after rebuild")
	}
	if c.taskComplete.Summary() != "finished it" {
		t.Fatalf("summary mismatch: got %q", c.taskComplete.Summary())
	}
}

// A task_complete that returned an error (e.g., subtasks incomplete) must
// NOT mark the task as completed on rebuild — the LLM rightly retried after
// seeing the error, and we shouldn't paper over that with stale state.
func TestRebuildTaskCompleteFromHistory_ErroredCallIgnored(t *testing.T) {
	c := &Commander{taskComplete: &aitools.TaskCompleteTool{}}

	msgs := []llm.Message{
		{Role: llm.RoleAssistant, Parts: []llm.ContentBlock{
			{Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{
				ID:    "call_bad",
				Name:  "task_complete",
				Input: json.RawMessage(`{"summary":"premature","succeed":true}`),
			}},
		}},
		{Role: llm.RoleUser, Parts: []llm.ContentBlock{
			{Type: llm.ContentTypeToolResult, ToolResult: &llm.ToolResultBlock{
				ToolUseID: "call_bad",
				Content:   `{"status":"error","error":"Cannot complete task: 2 of 3 subtasks are not yet complete."}`,
			}},
		}},
	}

	c.rebuildTaskCompleteFromHistory(msgs)

	if c.taskComplete.IsCompleted() {
		t.Fatal("erroring task_complete must not be treated as completed on rebuild")
	}
}

// Failed task_complete (succeed=false with reason) must restore the failure
// reason so the runner correctly marks the task as failed, not running.
func TestRebuildTaskCompleteFromHistory_FailedCompletion(t *testing.T) {
	c := &Commander{taskComplete: &aitools.TaskCompleteTool{}}

	msgs := []llm.Message{
		{Role: llm.RoleAssistant, Parts: []llm.ContentBlock{
			{Type: llm.ContentTypeToolUse, ToolUse: &llm.ToolUseBlock{
				ID:    "call_fail",
				Name:  "task_complete",
				Input: json.RawMessage(`{"succeed":false,"reason":"data unavailable"}`),
			}},
		}},
		{Role: llm.RoleUser, Parts: []llm.ContentBlock{
			{Type: llm.ContentTypeToolResult, ToolResult: &llm.ToolResultBlock{
				ToolUseID: "call_fail",
				Content:   `{"status":"ok","message":"Task marked as failed."}`,
			}},
		}},
	}

	c.rebuildTaskCompleteFromHistory(msgs)

	if !c.taskComplete.IsCompleted() {
		t.Fatal("failed-but-completed task should still be Completed after rebuild")
	}
	if c.taskComplete.IsSucceeded() {
		t.Fatal("succeeded should be false on a failure-completion")
	}
	if c.taskComplete.FailureReason() != "data unavailable" {
		t.Fatalf("failure reason lost: got %q", c.taskComplete.FailureReason())
	}
}

// History without any task_complete leaves state untouched.
func TestRebuildTaskCompleteFromHistory_NoCallNoChange(t *testing.T) {
	c := &Commander{taskComplete: &aitools.TaskCompleteTool{}}

	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "do it"},
		{Role: llm.RoleAssistant, Content: "thinking..."},
	}
	c.rebuildTaskCompleteFromHistory(msgs)

	if c.taskComplete.IsCompleted() {
		t.Fatal("no task_complete in history should leave state untouched")
	}
}

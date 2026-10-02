package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"squadron/aitools"
)

const memoryProposalToolName = "propose_mission_memory_update"

// MemoryProposal is a task worker's requested mutation, not a write. Only the
// harness may commit one after accepting the result and checking its grant.
type MemoryProposal struct {
	Key             string          `json:"key"`
	Value           json.RawMessage `json:"value"`
	ExpectedVersion uint64          `json:"expected_version"`
}

type memoryProposalTool struct{ grant TaskMemoryGrant }

func (*memoryProposalTool) ToolName() string { return memoryProposalToolName }
func (*memoryProposalTool) ToolDescription() string {
	return "Propose one versioned mission-memory update. This does not write immediately; the harness validates and commits it only if the task result is accepted. Use a key declared in the memory schema and its version from the task snapshot (0 for a new key)."
}
func (*memoryProposalTool) ToolReplaySafe() bool { return true }
func (*memoryProposalTool) ToolPayloadSchema() aitools.Schema {
	return (aitools.Schema{}).WithRawJSONSchema(json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"},"value":{},"expected_version":{"type":"integer","minimum":0}},"required":["key","value","expected_version"]}`))
}
func (t *memoryProposalTool) Call(_ context.Context, input string) string {
	var proposal MemoryProposal
	if err := json.Unmarshal([]byte(input), &proposal); err != nil {
		return memoryProposalToolError("invalid JSON input")
	}
	if err := validateMemoryProposal(&t.grant, proposal); err != nil {
		return memoryProposalToolError(err.Error())
	}
	return `{"status":"accepted"}`
}

func memoryProposalToolError(message string) string {
	encoded, _ := json.Marshal(struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}{Status: "error", Message: message})
	return string(encoded)
}

func validateMemoryProposal(grant *TaskMemoryGrant, proposal MemoryProposal) error {
	if grant == nil || grant.Access != "read_write" {
		return fmt.Errorf("task is not granted mission-memory writes")
	}
	if strings.TrimSpace(proposal.Key) == "" || len(proposal.Key) > 128 {
		return fmt.Errorf("memory key must be 1–128 bytes")
	}
	if len(proposal.Value) == 0 || len(proposal.Value) > 64*1024 || !json.Valid(proposal.Value) {
		return fmt.Errorf("memory value must be valid JSON of at most 64 KiB")
	}
	var field *ResultField
	for i := range grant.Fields {
		if grant.Fields[i].Name == proposal.Key {
			field = &grant.Fields[i]
			break
		}
	}
	if field == nil {
		return fmt.Errorf("memory key %q is not declared", proposal.Key)
	}
	wantVersion := uint64(0)
	for _, entry := range grant.Entries {
		if entry.Key == proposal.Key {
			wantVersion = entry.Version
			break
		}
	}
	if proposal.ExpectedVersion != wantVersion {
		return fmt.Errorf("memory key %q expected version %d, snapshot has %d", proposal.Key, proposal.ExpectedVersion, wantVersion)
	}
	var value any
	if err := json.Unmarshal(proposal.Value, &value); err != nil {
		return fmt.Errorf("decode memory value: %w", err)
	}
	if errors := validateResultValue(value, *field, "memory."+proposal.Key); len(errors) > 0 {
		return fmt.Errorf("%s", strings.Join(errors, "; "))
	}
	return nil
}

func memoryProposalsFromProgress(progress TaskAttemptProgress, grant *TaskMemoryGrant) ([]MemoryProposal, error) {
	ids := make([]string, 0, len(progress.Tools))
	for id := range progress.Tools {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var proposals []MemoryProposal
	seen := make(map[string]bool)
	for _, id := range ids {
		operation := progress.Tools[id]
		if operation.ToolName != memoryProposalToolName || operation.Status != ToolOperationCompleted || operation.IsError {
			continue
		}
		var outcome struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(operation.Result), &outcome); err != nil || outcome.Status != "accepted" {
			continue
		}
		var proposal MemoryProposal
		if err := json.Unmarshal([]byte(operation.InputJSON), &proposal); err != nil {
			return nil, fmt.Errorf("decode accepted memory proposal %q: %w", id, err)
		}
		if err := validateMemoryProposal(grant, proposal); err != nil {
			return nil, fmt.Errorf("validate accepted memory proposal %q: %w", id, err)
		}
		if seen[proposal.Key] {
			return nil, fmt.Errorf("task proposed memory key %q more than once", proposal.Key)
		}
		seen[proposal.Key] = true
		proposals = append(proposals, proposal)
	}
	return proposals, nil
}

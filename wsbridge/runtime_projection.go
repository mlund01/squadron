package wsbridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mlund01/squadron-wire/protocol"

	missionruntime "squadron/mission/runtime"
	"squadron/store"
)

// missionRuntimeProjection reads the durable checkpoint, not the legacy task
// tables. A nil projection means this run predates the mission-scoped runtime.
func missionRuntimeProjection(runtimeStore store.RuntimeStore, runID string) (*protocol.MissionRuntimeInfo, []store.RuntimeEventRecord, error) {
	if runtimeStore == nil {
		return nil, nil, nil
	}
	record, events, err := runtimeStore.LoadRuntime(runID)
	if err != nil {
		if errors.Is(err, store.ErrRuntimeNotFound) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("load mission runtime: %w", err)
	}
	var checkpoint missionruntime.Checkpoint
	if err := json.Unmarshal([]byte(record.PayloadJSON), &checkpoint); err != nil {
		return nil, nil, fmt.Errorf("decode mission runtime checkpoint: %w", err)
	}
	projection := &protocol.MissionRuntimeInfo{
		Revision: checkpoint.Revision,
		State:    string(checkpoint.RunState),
		Commander: protocol.MissionRuntimeCommanderInfo{
			ID:       checkpoint.Commander.ID,
			State:    string(checkpoint.Commander.State),
			Messages: make([]protocol.MissionRuntimeMessageInfo, 0, len(checkpoint.CommanderMessages)),
		},
		Tasks: make([]protocol.MissionRuntimeTaskInfo, 0, len(checkpoint.TaskStates)),
	}
	for _, message := range checkpoint.CommanderMessages {
		projection.Commander.Messages = append(projection.Commander.Messages, protocol.MissionRuntimeMessageInfo{
			ID: message.ID, Role: message.Role, Parts: runtimeContentParts(message.Parts), Model: runtimeModelTurn(message.Telemetry),
		})
	}
	taskNames := make([]string, 0, len(checkpoint.TaskStates))
	for name := range checkpoint.TaskStates {
		taskNames = append(taskNames, name)
	}
	sort.Strings(taskNames)
	for _, name := range taskNames {
		task := protocol.MissionRuntimeTaskInfo{
			Name: name, State: string(checkpoint.TaskStates[name]), Attempt: checkpoint.Attempts[name],
		}
		if result, ok := checkpoint.Results[name]; ok {
			task.Summary = result.Envelope.Summary
		}
		if failure, ok := checkpoint.Failures[name]; ok {
			task.Error = failure.Error
		}
		if deadline, ok := checkpoint.RetryDeadlines[name]; ok {
			value := deadline.UTC().Format(time.RFC3339Nano)
			task.RetryReadyAt = &value
		}
		for _, wait := range checkpoint.HumanInputWaits {
			if wait.TaskName != name {
				continue
			}
			human := &protocol.MissionRuntimeHumanInputInfo{
				Question: wait.Question, ShortSummary: wait.ShortSummary,
				RequestedAt: wait.RequestedAt.UTC().Format(time.RFC3339Nano),
			}
			if wait.DeadlineAt != nil {
				value := wait.DeadlineAt.UTC().Format(time.RFC3339Nano)
				human.DeadlineAt = &value
			}
			task.HumanInput = human
			break
		}
		for _, dispatch := range checkpoint.Dispatches {
			if dispatch.TaskName != name {
				continue
			}
			attempt := protocol.MissionRuntimeAttemptInfo{
				ID: dispatch.AttemptID, Number: dispatch.Attempt, Model: dispatch.Grant.Model,
				Tools: append([]string(nil), dispatch.Grant.Tools...), RunAs: dispatch.Grant.RunAs,
				DispatchedAt: dispatch.DispatchedAt.UTC().Format(time.RFC3339Nano),
			}
			if progress, ok := checkpoint.TaskProgress[dispatch.AttemptID]; ok {
				attempt.Phase = progress.Phase
				attempt.Messages = runtimeTaskMessages(dispatch.AttemptID, progress.Transcript)
				toolIDs := make([]string, 0, len(progress.Tools))
				for id := range progress.Tools {
					toolIDs = append(toolIDs, id)
				}
				sort.Strings(toolIDs)
				for _, id := range toolIDs {
					operation := progress.Tools[id]
					tool := protocol.MissionRuntimeToolOperationInfo{
						ToolUseID: operation.ToolUseID, ToolName: operation.ToolName,
						Status: string(operation.Status), IsError: operation.IsError,
					}
					if !operation.StartedAt.IsZero() {
						value := operation.StartedAt.UTC().Format(time.RFC3339Nano)
						tool.StartedAt = &value
					}
					attempt.ToolOperations = append(attempt.ToolOperations, tool)
				}
			}
			if result, ok := checkpoint.Results[name]; ok && result.Envelope.Provenance.AttemptID == dispatch.AttemptID {
				attempt.Phase = "completed"
				attempt.Messages = runtimeTaskMessages(dispatch.AttemptID, result.SessionTranscript)
			}
			task.Attempts = append(task.Attempts, attempt)
		}
		sort.Slice(task.Attempts, func(i, j int) bool { return task.Attempts[i].Number < task.Attempts[j].Number })
		projection.Tasks = append(projection.Tasks, task)
	}
	return projection, events, nil
}

// attributeRuntimeCosts uses the reporting ledger rather than estimating from
// tokens in the UI. The ledger is projected idempotently from durable model
// turns, so a recovered run does not count the same turn twice.
func attributeRuntimeCosts(projection *protocol.MissionRuntimeInfo, costs []store.TurnCostRecord) {
	if projection == nil {
		return
	}
	tasks := make(map[string]*protocol.MissionRuntimeTaskInfo, len(projection.Tasks))
	for index := range projection.Tasks {
		tasks[projection.Tasks[index].Name] = &projection.Tasks[index]
	}
	for _, cost := range costs {
		projection.TotalCost += cost.TotalCost
		if cost.Entity == "commander" {
			projection.Commander.Cost += cost.TotalCost
		} else if task := tasks[cost.TaskName]; task != nil {
			task.Cost += cost.TotalCost
		}
	}
}

func runtimeTaskMessages(attemptID string, messages []missionruntime.TaskSessionMessage) []protocol.MissionRuntimeMessageInfo {
	result := make([]protocol.MissionRuntimeMessageInfo, 0, len(messages))
	for index, message := range messages {
		result = append(result, protocol.MissionRuntimeMessageInfo{
			ID: fmt.Sprintf("%s:message:%d", attemptID, index), Role: message.Role,
			Parts: runtimeContentParts(message.Parts), Model: runtimeModelTurn(message.Telemetry),
		})
	}
	return result
}

func runtimeContentParts(parts []store.MessagePart) []protocol.MissionRuntimeContentPart {
	result := make([]protocol.MissionRuntimeContentPart, 0, len(parts))
	for _, part := range parts {
		result = append(result, protocol.MissionRuntimeContentPart{
			Type: part.Type, Text: part.Text, ToolUseID: part.ToolUseID,
			ToolName: part.ToolName, ToolInputJSON: part.ToolInputJSON,
			IsError: part.IsError, ImageData: part.ImageData,
			ImageMediaType: part.ImageMediaType, DocumentData: part.DocumentData,
			DocumentMediaType: part.DocumentMediaType, DocumentFilename: part.DocumentFilename,
		})
	}
	return result
}

func runtimeModelTurn(turn *missionruntime.ModelTurnTelemetry) *protocol.MissionRuntimeModelTurnInfo {
	if turn == nil {
		return nil
	}
	return &protocol.MissionRuntimeModelTurnInfo{
		ID: turn.ID, Model: turn.Model, InputTokens: turn.InputTokens,
		OutputTokens: turn.OutputTokens, CacheWriteTokens: turn.CacheWriteTokens,
		CacheReadTokens: turn.CacheReadTokens,
	}
}

package runtime

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"squadron/store"
)

// ResultRejectedError marks a mechanically invalid executor submission. It is
// safe for the coordinator to close the attempt through the retry policy;
// persistence errors are deliberately not represented by this type.
type ResultRejectedError struct{ Err error }

func (e *ResultRejectedError) Error() string { return "task result rejected: " + e.Err.Error() }
func (e *ResultRejectedError) Unwrap() error { return e.Err }

const (
	defaultResultSummaryLimit = 4 * 1024
	defaultResultPayloadLimit = 2 * 1024 * 1024
)

// ResultStatus is the executor-reported outcome. An accepted result may only
// advance the graph when it reports success and passes harness validation.
type ResultStatus string

const (
	ResultSucceeded ResultStatus = "succeeded"
	ResultFailed    ResultStatus = "failed"
)

// ArtifactReference points at rich output held by an artifact store. Artifact
// bytes are never embedded in the mission checkpoint.
type ArtifactReference struct {
	ID        string `json:"id"`
	URI       string `json:"uri"`
	Name      string `json:"name,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

// ResultProvenance attributes an exact executor result to its immutable
// attempt and configuration.
type ResultProvenance struct {
	RunID                string `json:"runId"`
	TaskName             string `json:"taskName"`
	AttemptID            string `json:"attemptId"`
	Model                string `json:"model"`
	RunAs                string `json:"runAs"`
	ConfigurationVersion string `json:"configurationVersion"`
}

// TaskResultEnvelope is the provider-neutral result produced by an ephemeral
// task executor. RawOutput preserves the exact final model response for audit;
// Content is the useful rich response exposed to operators and the commander.
type TaskResultEnvelope struct {
	Status      ResultStatus        `json:"status"`
	Summary     string              `json:"summary"`
	Content     []store.MessagePart `json:"content,omitempty"`
	Artifacts   []ArtifactReference `json:"artifacts,omitempty"`
	Data        json.RawMessage     `json:"data,omitempty"`
	Diagnostics json.RawMessage     `json:"diagnostics,omitempty"`
	RawOutput   []store.MessagePart `json:"rawOutput"`
	Provenance  ResultProvenance    `json:"provenance"`
	// SessionTranscript is populated by the model-session adapter for durable
	// continuation. It is moved into AcceptedTaskResult during validation and
	// is never included in the executor-visible result payload.
	SessionTranscript []TaskSessionMessage `json:"-"`
}

// TaskSessionMessage is one provider-neutral message in a task worker's
// logical session. The transcript is runtime state, not task output: it is
// retained only so a bounded clarification can restore the exact session.
type TaskSessionMessage struct {
	Role  string              `json:"role"`
	Parts []store.MessagePart `json:"parts"`
}

// ResultField is the deterministic typed-data contract for a task. A nil or
// empty schema intentionally permits natural-language and rich-only results.
type ResultField struct {
	Name       string        `json:"name"`
	Type       string        `json:"type"`
	Required   bool          `json:"required,omitempty"`
	Items      *ResultField  `json:"items,omitempty"`
	Properties []ResultField `json:"properties,omitempty"`
}

// ResultPolicy is snapshotted with the mission definition so result
// acceptance is reproducible after a restart.
type ResultPolicy struct {
	Fields          []ResultField `json:"fields,omitempty"`
	MaxPayloadBytes int           `json:"maxPayloadBytes,omitempty"`
	ArtifactSchemes []string      `json:"artifactSchemes,omitempty"`
}

// ResultValidation records the mechanical decision that allowed (or refused)
// a result to advance the graph.
type ResultValidation struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors,omitempty"`
}

// AcceptedTaskResult is persisted in the run checkpoint and completion event.
type AcceptedTaskResult struct {
	Envelope          TaskResultEnvelope   `json:"envelope"`
	Validation        ResultValidation     `json:"validation"`
	SessionTranscript []TaskSessionMessage `json:"sessionTranscript,omitempty"`
}

func validateTaskResult(dispatch Dispatch, policy ResultPolicy, envelope TaskResultEnvelope) (AcceptedTaskResult, error) {
	envelope = cloneTaskResultEnvelope(envelope)
	errors := make([]string, 0)
	if envelope.Status != ResultSucceeded {
		errors = append(errors, fmt.Sprintf("result status must be %q", ResultSucceeded))
	}
	if strings.TrimSpace(envelope.Summary) == "" {
		errors = append(errors, "result summary is required")
	} else if utf8.RuneCountInString(envelope.Summary) > defaultResultSummaryLimit {
		errors = append(errors, fmt.Sprintf("result summary exceeds %d characters", defaultResultSummaryLimit))
	}
	wantProvenance := ResultProvenance{
		RunID: dispatch.RunID, TaskName: dispatch.TaskName, AttemptID: dispatch.AttemptID,
		Model: dispatch.Grant.Model, RunAs: dispatch.Grant.RunAs, ConfigurationVersion: dispatch.Grant.ConfigurationVersion,
	}
	if envelope.Provenance != wantProvenance {
		errors = append(errors, "result provenance does not match the active dispatch")
	}
	if len(envelope.RawOutput) == 0 {
		errors = append(errors, "exact raw output is required")
	}
	if len(envelope.Data) > 0 && !json.Valid(envelope.Data) {
		errors = append(errors, "result data is not valid JSON")
	}
	if len(policy.Fields) > 0 {
		if len(envelope.Data) == 0 {
			errors = append(errors, "typed result data is required by the task output schema")
		} else if json.Valid(envelope.Data) {
			var value any
			if err := json.Unmarshal(envelope.Data, &value); err != nil {
				errors = append(errors, "typed result data cannot be decoded")
			} else {
				errors = append(errors, validateResultObject(value, policy.Fields, "data")...)
			}
		}
	}
	allowedSchemes := policy.ArtifactSchemes
	if len(allowedSchemes) == 0 {
		allowedSchemes = []string{"artifact"}
	}
	for index, artifact := range envelope.Artifacts {
		if artifact.ID == "" || artifact.URI == "" {
			errors = append(errors, fmt.Sprintf("artifact %d requires id and uri", index))
			continue
		}
		parsed, err := url.Parse(artifact.URI)
		if err != nil || parsed.Scheme == "" || !containsString(allowedSchemes, parsed.Scheme) {
			errors = append(errors, fmt.Sprintf("artifact %q uses a disallowed URI", artifact.ID))
		}
	}
	limit := policy.MaxPayloadBytes
	if limit <= 0 {
		limit = defaultResultPayloadLimit
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		errors = append(errors, fmt.Sprintf("result cannot be encoded: %v", err))
	} else if len(encoded) > limit {
		errors = append(errors, fmt.Sprintf("result payload exceeds %d bytes", limit))
	}
	validation := ResultValidation{Valid: len(errors) == 0, Errors: errors}
	transcript := cloneTaskSessionMessages(envelope.SessionTranscript)
	envelope.SessionTranscript = nil
	accepted := AcceptedTaskResult{Envelope: envelope, Validation: validation, SessionTranscript: transcript}
	if !validation.Valid {
		return accepted, fmt.Errorf("task result rejected: %s", strings.Join(errors, "; "))
	}
	return accepted, nil
}

func validateResultObject(value any, fields []ResultField, path string) []string {
	object, ok := value.(map[string]any)
	if !ok {
		return []string{path + " must be an object"}
	}
	var errors []string
	for _, field := range fields {
		child, exists := object[field.Name]
		if !exists {
			if field.Required {
				errors = append(errors, path+"."+field.Name+" is required")
			}
			continue
		}
		errors = append(errors, validateResultValue(child, field, path+"."+field.Name)...)
	}
	return errors
}

func validateResultValue(value any, field ResultField, path string) []string {
	switch field.Type {
	case "string":
		if _, ok := value.(string); !ok {
			return []string{path + " must be a string"}
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return []string{path + " must be a number"}
		}
	case "integer":
		n, ok := value.(float64)
		if !ok || n != float64(int64(n)) {
			return []string{path + " must be an integer"}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return []string{path + " must be a boolean"}
		}
	case "array", "list":
		items, ok := value.([]any)
		if !ok {
			return []string{path + " must be an array"}
		}
		if field.Items != nil {
			var errors []string
			for i, item := range items {
				errors = append(errors, validateResultValue(item, *field.Items, fmt.Sprintf("%s[%d]", path, i))...)
			}
			return errors
		}
	case "object", "map":
		if len(field.Properties) > 0 {
			return validateResultObject(value, field.Properties, path)
		}
		if _, ok := value.(map[string]any); !ok {
			return []string{path + " must be an object"}
		}
	default:
		return []string{fmt.Sprintf("%s uses unsupported schema type %q", path, field.Type)}
	}
	return nil
}

func cloneTaskResultEnvelope(result TaskResultEnvelope) TaskResultEnvelope {
	result.Content = append([]store.MessagePart(nil), result.Content...)
	result.RawOutput = append([]store.MessagePart(nil), result.RawOutput...)
	result.Artifacts = append([]ArtifactReference(nil), result.Artifacts...)
	result.Data = append(json.RawMessage(nil), result.Data...)
	result.Diagnostics = append(json.RawMessage(nil), result.Diagnostics...)
	result.SessionTranscript = cloneTaskSessionMessages(result.SessionTranscript)
	return result
}

func cloneAcceptedResults(source map[string]AcceptedTaskResult) map[string]AcceptedTaskResult {
	result := make(map[string]AcceptedTaskResult, len(source))
	for key, accepted := range source {
		accepted.Envelope = cloneTaskResultEnvelope(accepted.Envelope)
		accepted.Validation.Errors = append([]string(nil), accepted.Validation.Errors...)
		accepted.SessionTranscript = cloneTaskSessionMessages(accepted.SessionTranscript)
		result[key] = accepted
	}
	return result
}

func cloneTaskSessionMessages(messages []TaskSessionMessage) []TaskSessionMessage {
	if messages == nil {
		return nil
	}
	result := make([]TaskSessionMessage, len(messages))
	for index, message := range messages {
		result[index] = TaskSessionMessage{Role: message.Role, Parts: append([]store.MessagePart(nil), message.Parts...)}
	}
	return result
}

func cloneResultPolicy(policy ResultPolicy) ResultPolicy {
	policy.Fields = cloneResultFields(policy.Fields)
	policy.ArtifactSchemes = append([]string(nil), policy.ArtifactSchemes...)
	return policy
}

func cloneResultFields(fields []ResultField) []ResultField {
	result := make([]ResultField, len(fields))
	for index, field := range fields {
		result[index] = field
		result[index].Properties = cloneResultFields(field.Properties)
		if field.Items != nil {
			item := cloneResultFields([]ResultField{*field.Items})[0]
			result[index].Items = &item
		}
	}
	return result
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

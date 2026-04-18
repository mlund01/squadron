package streamers

import "github.com/mlund01/squadron-wire/protocol"

// EventMissionIssue is the event type for advisory issues raised during a
// mission run — budget breaches, provider outages, tool errors, etc. It is
// defined locally (not in squadron-wire yet) so we can iterate on the shape
// without a wire release; the command center treats unknown event types as
// opaque envelopes, so forwarding still works end-to-end.
const EventMissionIssue protocol.MissionEventType = "mission_issue"

// IssueSeverity describes how loud an issue should be in the UI and whether
// it will terminate the mission.
//
// Issues are *advisory*: emitting `IssueFatal` does not itself fail the
// mission. When a fatal issue fires there is (by construction) also an error
// returning from the same source, and the runner's existing error path is
// what actually transitions the mission to "failed". Keeping those two paths
// separate lets us emit warning-level issues (retryable provider errors, etc.)
// without having to thread an error into every callsite — and it keeps the
// command center's picture of "why did this fail?" in lockstep with the
// authoritative error because both come from the same breach point.
type IssueSeverity string

const (
	// IssueWarning is informational — work continues, often a retry is in flight.
	IssueWarning IssueSeverity = "warning"
	// IssueError marks a non-retryable failure in a bounded scope (e.g. a single
	// tool call) that the mission may nevertheless recover from.
	IssueError IssueSeverity = "error"
	// IssueFatal marks an issue that will terminate the mission. The authoritative
	// termination still comes from the error returned by the originating code —
	// this is just the corresponding observability signal.
	IssueFatal IssueSeverity = "fatal"
)

// Issue categories. Keep these stable — they're part of the event contract.
const (
	IssueCategoryBudgetExceeded = "budget_exceeded"
	IssueCategoryProviderError  = "provider_error"
	IssueCategoryToolError      = "tool_error"
)

// MissionIssueData is the payload for a mission_issue event. Category and
// Details are the structured half; Message is the human-readable half. Both
// are intentionally free-form so handlers can grow the taxonomy without a
// wire bump.
type MissionIssueData struct {
	Severity IssueSeverity  `json:"severity"`
	Category string         `json:"category"`
	Message  string         `json:"message"`
	TaskName string         `json:"taskName,omitempty"`
	Entity   string         `json:"entity,omitempty"` // "commander" | "<agent_name>"
	Retrying bool           `json:"retrying,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
}

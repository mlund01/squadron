package humaninput

import (
	"database/sql"
	"errors"
	"fmt"

	"squadron/store"
)

// ResolveOutcome describes the result of a Resolve call. The two
// boolean flags allow callers (wsbridge wire handler, gateway
// SquadronAPI surface) to distinguish "first to resolve", "already
// resolved", and "no such request" without forcing them through error
// matching for the common cases.
type ResolveOutcome struct {
	Record          store.HumanInputRequestRecord
	AlreadyResolved bool
	NotFound        bool
}

// Listener is anything that can be notified that a specific
// tool-call's resolution has landed. The wsbridge AskHuman call
// blocks on a per-tool-call listener channel until this fires.
//
// Implementations must be safe to invoke from any goroutine and may
// be called more than once for the same toolCallID (e.g. if a
// re-delivered event sneaks through); they should be idempotent.
type Listener interface {
	DeliverResolution(toolCallID, response, responderUserID string)
}

// Resolve writes a resolution to the store, notifies the in-process
// listener (so the agent's blocking AskHuman call unblocks), and
// publishes a resolved Event to the notifier (so gateways and other
// observers see the change).
//
// All three side-effects are run in order; a failure on the store
// write aborts the rest, but a successful store write is followed by
// best-effort listener delivery and notifier publish.
func Resolve(
	stores *store.Bundle,
	listener Listener,
	notifier *Notifier,
	toolCallID, response, responderUserID string,
) (ResolveOutcome, error) {
	if stores == nil || stores.HumanInputs == nil {
		return ResolveOutcome{}, fmt.Errorf("resolve: no human input store configured")
	}

	// Snapshot the existing state to detect already-resolved without
	// guessing from ResolveRequest's return shape.
	existing, err := stores.HumanInputs.GetByToolCallID(toolCallID)
	if errors.Is(err, sql.ErrNoRows) {
		return ResolveOutcome{NotFound: true}, nil
	}
	if err != nil {
		return ResolveOutcome{}, fmt.Errorf("resolve: lookup: %w", err)
	}
	if existing.State == store.HumanInputStateResolved {
		return ResolveOutcome{
			Record:          *existing,
			AlreadyResolved: true,
		}, nil
	}

	resolved, err := stores.HumanInputs.ResolveRequest(toolCallID, response, responderUserID)
	if err != nil {
		return ResolveOutcome{}, fmt.Errorf("resolve: %w", err)
	}

	// Listener and notifier are best-effort and don't surface errors —
	// once the store has the resolution, downstream observers are
	// strictly informational.
	if listener != nil {
		listener.DeliverResolution(toolCallID, response, responderUserID)
	}
	if notifier != nil {
		notifier.Publish(Event{Kind: EventKindResolved, Record: *resolved})
	}

	return ResolveOutcome{Record: *resolved}, nil
}

// PublishCreated is a thin helper used by the AskHuman emit path so
// it can fire one consistent Event without callers reaching for the
// struct shape every time.
func PublishCreated(notifier *Notifier, rec store.HumanInputRequestRecord) {
	if notifier == nil {
		return
	}
	notifier.Publish(Event{Kind: EventKindCreated, Record: rec})
}

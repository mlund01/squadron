package humaninput

import (
	"database/sql"
	"errors"
	"fmt"

	"squadron/store"
)

// ResolveOutcome lets callers distinguish "first to resolve",
// "already resolved", and "no such request" without errors.Is matching
// — the latter two are non-error outcomes.
type ResolveOutcome struct {
	Record          store.HumanInputRequestRecord
	AlreadyResolved bool
	AlreadyExpired  bool
	NotFound        bool
}

// Listener wakes a blocking AskHuman call when its tool call resolves.
// Must be safe under concurrent calls and idempotent on re-delivery.
type Listener interface {
	DeliverResolution(toolCallID, response, responderUserID string)
}

// Resolve writes the resolution, wakes the listener, publishes to the
// notifier. Listener + notifier are best-effort; once the store
// commits, downstream observers are informational only.
func Resolve(
	stores *store.Bundle,
	listener Listener,
	notifier *Notifier,
	toolCallID, response, responderUserID string,
) (ResolveOutcome, error) {
	if stores == nil || stores.HumanInputs == nil {
		return ResolveOutcome{}, fmt.Errorf("resolve: no human input store configured")
	}

	// Snapshot existing state — ResolveRequest's UPDATE returns no row
	// info that distinguishes already-resolved from first-resolve.
	existing, err := stores.HumanInputs.GetByToolCallID(toolCallID)
	if errors.Is(err, sql.ErrNoRows) {
		return ResolveOutcome{NotFound: true}, nil
	}
	if err != nil {
		return ResolveOutcome{}, fmt.Errorf("resolve: lookup: %w", err)
	}
	if existing.State == store.HumanInputStateResolved {
		return ResolveOutcome{Record: *existing, AlreadyResolved: true}, nil
	}
	if existing.State == store.HumanInputStateExpired {
		return ResolveOutcome{Record: *existing, AlreadyExpired: true}, nil
	}

	resolved, err := stores.HumanInputs.ResolveRequest(toolCallID, response, responderUserID)
	if err != nil {
		return ResolveOutcome{}, fmt.Errorf("resolve: %w", err)
	}
	if resolved.State == store.HumanInputStateExpired {
		return ResolveOutcome{Record: *resolved, AlreadyExpired: true}, nil
	}
	if resolved.State != store.HumanInputStateResolved {
		return ResolveOutcome{}, fmt.Errorf("resolve: request %q remains in state %q", toolCallID, resolved.State)
	}
	if resolved.Response == nil || *resolved.Response != response || resolved.ResponderUserID == nil || *resolved.ResponderUserID != responderUserID {
		return ResolveOutcome{Record: *resolved, AlreadyResolved: true}, nil
	}
	if listener != nil {
		listener.DeliverResolution(toolCallID, response, responderUserID)
	}
	if notifier != nil {
		notifier.Publish(Event{Kind: EventKindResolved, Record: *resolved})
	}
	return ResolveOutcome{Record: *resolved}, nil
}

func PublishCreated(notifier *Notifier, rec store.HumanInputRequestRecord) {
	if notifier == nil {
		return
	}
	notifier.Publish(Event{Kind: EventKindCreated, Record: rec})
}

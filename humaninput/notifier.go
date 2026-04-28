// Package humaninput provides the in-process publish/subscribe hub for
// builtins.human.ask events. The wsbridge AskHuman path and the
// gateway runtime both publish to this notifier; consumers (gateway
// dispatcher, future commander-side observers) subscribe to it.
//
// Splitting this from wsbridge avoids an import cycle: gateway needs
// to consume events but cannot import wsbridge (which needs the
// gateway to forward events).
package humaninput

import (
	"sync"

	"squadron/store"
)

// EventKind classifies a notifier event.
type EventKind string

const (
	EventKindCreated  EventKind = "created"
	EventKindResolved EventKind = "resolved"
)

// Event is a single notifier delivery — a state change on a single
// human-input request.
type Event struct {
	Kind   EventKind
	Record store.HumanInputRequestRecord
}

// Notifier fans events out to subscribers in process. Best-effort
// delivery: a slow subscriber drops events rather than backing up the
// publisher, on the principle that subscribers can always ListRequests
// to catch up if they fall behind.
type Notifier struct {
	mu   sync.Mutex
	subs map[chan<- Event]struct{}
}

// New constructs an empty notifier ready for Subscribe / Publish.
func New() *Notifier {
	return &Notifier{subs: make(map[chan<- Event]struct{})}
}

// Subscribe returns a channel that receives every event published
// after the call returns. The returned cancel func deregisters the
// subscription and closes the channel; callers should always defer it.
//
// The channel is buffered (cap 32). Slow consumers see drops rather
// than back-pressure on the publisher.
func (n *Notifier) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 32)
	n.mu.Lock()
	n.subs[ch] = struct{}{}
	n.mu.Unlock()

	cancel := func() {
		n.mu.Lock()
		if _, ok := n.subs[ch]; ok {
			delete(n.subs, ch)
			close(ch)
		}
		n.mu.Unlock()
	}
	return ch, cancel
}

// Publish fans an event out to every active subscriber. Holds the
// lock for the entire iteration so a concurrent unsubscribe can't
// close a subscriber's channel mid-send.
func (n *Notifier) Publish(ev Event) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for ch := range n.subs {
		select {
		case ch <- ev:
		default:
			// slow subscriber — drop
		}
	}
}

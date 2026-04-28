// Package humaninput is the in-process pub/sub hub for human.ask
// events. Lives in its own package to break the wsbridge ↔ gateway
// import cycle (both publish, both subscribe).
package humaninput

import (
	"sync"

	"squadron/store"
)

type EventKind string

const (
	EventKindCreated  EventKind = "created"
	EventKindResolved EventKind = "resolved"
)

type Event struct {
	Kind   EventKind
	Record store.HumanInputRequestRecord
}

// Notifier fans events out to subscribers. Slow subscribers drop
// rather than back-pressure the publisher — a fallen-behind consumer
// can always re-sync via ListRequests.
type Notifier struct {
	mu   sync.Mutex
	subs map[chan<- Event]struct{}
}

func New() *Notifier {
	return &Notifier{subs: make(map[chan<- Event]struct{})}
}

// Subscribe returns a buffered channel (cap 32) and a cancel func.
// Callers must defer the cancel.
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

// Publish holds the lock through the fan-out so a concurrent
// unsubscribe can't close a channel mid-send.
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

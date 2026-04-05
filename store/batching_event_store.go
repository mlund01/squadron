package store

import (
	"log"
	"sync"
	"time"
)

const (
	defaultBatchSize     = 50
	defaultFlushInterval = 500 * time.Millisecond
)

// BatchingEventStore wraps an EventStore and buffers StoreEvent calls,
// flushing them in batches either when the buffer reaches maxBatchSize
// or when the flush interval elapses — whichever comes first.
type BatchingEventStore struct {
	inner         EventStore
	maxBatchSize  int
	flushInterval time.Duration

	mu      sync.Mutex
	buf     []MissionEvent
	timer   *time.Timer
	closed  bool
	flushCh chan struct{} // signals flush goroutine
	doneCh  chan struct{} // closed when flush goroutine exits
}

// NewBatchingEventStore creates a batching wrapper around an existing EventStore.
func NewBatchingEventStore(inner EventStore) *BatchingEventStore {
	return NewBatchingEventStoreWithOptions(inner, defaultBatchSize, defaultFlushInterval)
}

// NewBatchingEventStoreWithOptions creates a batching wrapper with custom settings.
func NewBatchingEventStoreWithOptions(inner EventStore, maxBatchSize int, flushInterval time.Duration) *BatchingEventStore {
	b := &BatchingEventStore{
		inner:         inner,
		maxBatchSize:  maxBatchSize,
		flushInterval: flushInterval,
		buf:           make([]MissionEvent, 0, maxBatchSize),
		flushCh:       make(chan struct{}, 1),
		doneCh:        make(chan struct{}),
	}
	go b.flushLoop()
	return b
}

// StoreEvent buffers a single event. If the buffer reaches maxBatchSize,
// it triggers an immediate flush.
func (b *BatchingEventStore) StoreEvent(event MissionEvent) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return b.inner.StoreEvent(event)
	}
	b.buf = append(b.buf, event)
	needsFlush := len(b.buf) >= b.maxBatchSize

	// Start the timer on first buffered event
	if len(b.buf) == 1 && b.timer == nil {
		b.timer = time.AfterFunc(b.flushInterval, func() {
			select {
			case b.flushCh <- struct{}{}:
			default:
			}
		})
	}
	b.mu.Unlock()

	if needsFlush {
		select {
		case b.flushCh <- struct{}{}:
		default:
		}
	}
	return nil
}

// StoreEvents passes through to the underlying store directly.
func (b *BatchingEventStore) StoreEvents(events []MissionEvent) error {
	return b.inner.StoreEvents(events)
}

// GetEventsByMission flushes pending events, then queries.
func (b *BatchingEventStore) GetEventsByMission(missionID string, limit, offset int) ([]MissionEvent, error) {
	b.flush()
	return b.inner.GetEventsByMission(missionID, limit, offset)
}

// GetEventsByTask flushes pending events, then queries.
func (b *BatchingEventStore) GetEventsByTask(taskID string, limit, offset int) ([]MissionEvent, error) {
	b.flush()
	return b.inner.GetEventsByTask(taskID, limit, offset)
}

// Flush writes any buffered events to the underlying store immediately.
func (b *BatchingEventStore) Flush() {
	b.flush()
}

// Close flushes remaining events and stops the background goroutine.
func (b *BatchingEventStore) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.mu.Unlock()

	// Signal flush goroutine to exit
	close(b.flushCh)
	<-b.doneCh

	// Final flush of any remaining events
	b.flush()
}

func (b *BatchingEventStore) flush() {
	b.mu.Lock()
	if len(b.buf) == 0 {
		b.mu.Unlock()
		return
	}
	batch := b.buf
	b.buf = make([]MissionEvent, 0, b.maxBatchSize)
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.mu.Unlock()

	if err := b.inner.StoreEvents(batch); err != nil {
		log.Printf("BatchingEventStore: flush %d events: %v", len(batch), err)
	}
}

func (b *BatchingEventStore) flushLoop() {
	defer close(b.doneCh)
	for range b.flushCh {
		b.mu.Lock()
		closed := b.closed
		b.mu.Unlock()
		if closed {
			return
		}
		b.flush()
	}
}

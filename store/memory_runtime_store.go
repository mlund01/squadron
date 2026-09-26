package store

import (
	"fmt"
	"sync"
)

// MemoryRuntimeStore is a deterministic RuntimeStore for tests and embedded
// runtimes. Database implementations use the same atomic contract.
type MemoryRuntimeStore struct {
	mu          sync.RWMutex
	checkpoints map[string]RuntimeCheckpointRecord
	events      map[string][]RuntimeEventRecord
}

func NewMemoryRuntimeStore() *MemoryRuntimeStore {
	return &MemoryRuntimeStore{
		checkpoints: make(map[string]RuntimeCheckpointRecord),
		events:      make(map[string][]RuntimeEventRecord),
	}
}

func (s *MemoryRuntimeStore) CreateRuntime(checkpoint RuntimeCheckpointRecord, events []RuntimeEventRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.checkpoints[checkpoint.RunID]; exists {
		return fmt.Errorf("run %q already exists", checkpoint.RunID)
	}
	s.checkpoints[checkpoint.RunID] = checkpoint
	s.events[checkpoint.RunID] = assignRuntimeSequences(nil, events)
	return nil
}

func (s *MemoryRuntimeStore) CommitRuntime(runID string, expectedRevision uint64, checkpoint RuntimeCheckpointRecord, events []RuntimeEventRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.checkpoints[runID]
	if !exists {
		return fmt.Errorf("run %q not found", runID)
	}
	if current.Revision != expectedRevision {
		return fmt.Errorf("run %q revision conflict: got %d, want %d", runID, current.Revision, expectedRevision)
	}
	if checkpoint.RunID != runID {
		return fmt.Errorf("checkpoint belongs to run %q, want %q", checkpoint.RunID, runID)
	}
	s.checkpoints[runID] = checkpoint
	s.events[runID] = append(s.events[runID], assignRuntimeSequences(s.events[runID], events)...)
	return nil
}

func (s *MemoryRuntimeStore) LoadRuntime(runID string) (RuntimeCheckpointRecord, []RuntimeEventRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	checkpoint, exists := s.checkpoints[runID]
	if !exists {
		return RuntimeCheckpointRecord{}, nil, fmt.Errorf("run %q: %w", runID, ErrRuntimeNotFound)
	}
	events := append([]RuntimeEventRecord(nil), s.events[runID]...)
	return checkpoint, events, nil
}

func assignRuntimeSequences(existing, appended []RuntimeEventRecord) []RuntimeEventRecord {
	result := make([]RuntimeEventRecord, len(appended))
	next := uint64(len(existing) + 1)
	for index, event := range appended {
		event.Sequence = next + uint64(index)
		result[index] = event
	}
	return result
}

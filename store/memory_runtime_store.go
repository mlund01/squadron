package store

import (
	"fmt"
	"sort"
	"sync"
)

// MemoryRuntimeStore is a deterministic RuntimeStore for tests and embedded
// runtimes. Database implementations use the same atomic contract.
type MemoryRuntimeStore struct {
	mu          sync.RWMutex
	checkpoints map[string]RuntimeCheckpointRecord
	events      map[string][]RuntimeEventRecord
	memory      map[string]MissionMemoryEntry
}

func NewMemoryRuntimeStore() *MemoryRuntimeStore {
	return &MemoryRuntimeStore{
		checkpoints: make(map[string]RuntimeCheckpointRecord),
		events:      make(map[string][]RuntimeEventRecord),
		memory:      make(map[string]MissionMemoryEntry),
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
	return s.CommitRuntimeWithMemory(runID, expectedRevision, checkpoint, events, nil)
}

func (s *MemoryRuntimeStore) CommitRuntimeWithMemory(runID string, expectedRevision uint64, checkpoint RuntimeCheckpointRecord, events []RuntimeEventRecord, writes []MissionMemoryWrite) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateMissionMemoryWrites(writes); err != nil {
		return err
	}
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
	for _, write := range writes {
		current := s.memory[write.MissionName+"\x00"+write.Key]
		if current.Version != write.ExpectedVersion {
			return fmt.Errorf("mission memory %q version conflict: expected %d", write.Key, write.ExpectedVersion)
		}
	}
	s.checkpoints[runID] = checkpoint
	s.events[runID] = append(s.events[runID], assignRuntimeSequences(s.events[runID], events)...)
	for _, write := range writes {
		s.memory[write.MissionName+"\x00"+write.Key] = MissionMemoryEntry{
			MissionName: write.MissionName, Key: write.Key, ValueJSON: write.ValueJSON,
			Version: write.ExpectedVersion + 1, ExpiresAt: write.ExpiresAt,
		}
	}
	return nil
}

func (s *MemoryRuntimeStore) LoadMissionMemory(missionName string) ([]MissionMemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if missionName == "" {
		return nil, fmt.Errorf("mission name is required")
	}
	var entries []MissionMemoryEntry
	for _, entry := range s.memory {
		if entry.MissionName == missionName {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	return entries, nil
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

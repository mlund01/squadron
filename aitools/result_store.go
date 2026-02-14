package aitools

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

// ResultType identifies the type of stored result
type ResultType string

const (
	ResultTypeArray  ResultType = "array"
	ResultTypeObject ResultType = "object"
	ResultTypeText   ResultType = "text"
)

// StoredResult holds a large result with metadata
type StoredResult struct {
	ID       string
	ToolName string
	Type     ResultType
	Size     int            // Item count for arrays, byte length for text/objects
	RawData  string         // Original string data
	Array    []any          // Parsed array (if Type == array)
	Object   map[string]any // Parsed object (if Type == object)
}

// ResultStore stores large tool results for later retrieval
type ResultStore interface {
	// Store saves a result and returns its ID
	Store(toolName string, result StoredResult) string

	// Get retrieves a stored result by ID
	Get(id string) (*StoredResult, bool)

	// GetInfo returns metadata about all stored results
	GetInfo() []ResultInfo
}

// ResultInfo provides metadata about a stored result
type ResultInfo struct {
	ID   string
	Type ResultType
	Size int
}

// MemoryResultStore is a simple in-memory implementation of ResultStore
type MemoryResultStore struct {
	mu      sync.RWMutex
	results map[string]*StoredResult
	seqNum  int64
}

// NewMemoryResultStore creates a new in-memory result store
func NewMemoryResultStore() *MemoryResultStore {
	return &MemoryResultStore{
		results: make(map[string]*StoredResult),
	}
}

// Store saves a result and returns its ID
func (s *MemoryResultStore) Store(toolName string, result StoredResult) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	seq := atomic.AddInt64(&s.seqNum, 1)
	id := fmt.Sprintf("_result_%s_%d", sanitizeName(toolName), seq)
	result.ID = id
	result.ToolName = toolName
	s.results[id] = &result
	return id
}

// Get retrieves a stored result by ID
func (s *MemoryResultStore) Get(id string) (*StoredResult, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.results[id]
	return r, ok
}

// GetInfo returns metadata about all stored results
func (s *MemoryResultStore) GetInfo() []ResultInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	infos := make([]ResultInfo, 0, len(s.results))
	for _, r := range s.results {
		infos = append(infos, ResultInfo{
			ID:   r.ID,
			Type: r.Type,
			Size: r.Size,
		})
	}
	return infos
}

// GetAll returns all stored results
func (s *MemoryResultStore) GetAll() []*StoredResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]*StoredResult, 0, len(s.results))
	for _, r := range s.results {
		results = append(results, r)
	}
	return results
}

// sanitizeName replaces characters that shouldn't appear in result IDs
func sanitizeName(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, ".", "_"), "-", "_")
}

package runtime

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

const maxCommanderMemoryPageBytes = 16 * 1024

// InspectMemoryRequest is a read-only commander control. ExpectedVersion
// pins subsequent pages to the version returned with the first page.
type InspectMemoryRequest struct {
	RequestID       string
	Key             string
	Revision        uint64
	Offset          int
	ExpectedVersion *uint64
}

// CommanderMemoryInspection contains one bounded page of a declared memory
// key. Chunk is a slice of its JSON representation, not a new JSON value.
type CommanderMemoryInspection struct {
	RequestID  string `json:"request_id"`
	Key        string `json:"key"`
	Version    uint64 `json:"version"`
	Exists     bool   `json:"exists"`
	Chunk      string `json:"chunk,omitempty"`
	Offset     int    `json:"offset,omitempty"`
	TotalBytes int    `json:"total_bytes,omitempty"`
	NextOffset int    `json:"next_offset,omitempty"`
	Stale      bool   `json:"stale,omitempty"`
}

// CommanderMemoryReader grants scoped, read-only access to keys declared by
// the mission. It never exposes the underlying store or a mutation method.
type CommanderMemoryReader interface {
	ReadCommanderMemory(key string) (MemoryValueSnapshot, error)
}

func pageCommanderMemory(request InspectMemoryRequest, value MemoryValueSnapshot) (CommanderMemoryInspection, error) {
	if request.RequestID == "" || request.Key == "" || value.Key != request.Key {
		return CommanderMemoryInspection{}, fmt.Errorf("memory inspection requires a matching key and request ID")
	}
	if request.Offset < 0 || (request.Offset > 0 && request.ExpectedVersion == nil) {
		return CommanderMemoryInspection{}, fmt.Errorf("memory inspection continuation requires an offset and expected version")
	}
	result := CommanderMemoryInspection{RequestID: request.RequestID, Key: request.Key, Version: value.Version}
	if request.ExpectedVersion != nil && *request.ExpectedVersion != value.Version {
		result.Stale = true
		return result, nil
	}
	if len(value.Value) == 0 {
		if request.Offset != 0 {
			return CommanderMemoryInspection{}, fmt.Errorf("memory key %q has no value to page", request.Key)
		}
		return result, nil
	}
	if !json.Valid(value.Value) {
		return CommanderMemoryInspection{}, fmt.Errorf("memory key %q contains invalid JSON", request.Key)
	}
	if request.Offset >= len(value.Value) {
		return CommanderMemoryInspection{}, fmt.Errorf("memory offset %d exceeds value size %d", request.Offset, len(value.Value))
	}
	end := min(len(value.Value), request.Offset+maxCommanderMemoryPageBytes)
	for end < len(value.Value) && end > request.Offset && !utf8.RuneStart(value.Value[end]) {
		end--
	}
	if end == request.Offset {
		return CommanderMemoryInspection{}, fmt.Errorf("memory page cannot fit one character")
	}
	result.Exists = true
	result.Chunk = string(value.Value[request.Offset:end])
	result.Offset = request.Offset
	result.TotalBytes = len(value.Value)
	if end < len(value.Value) {
		result.NextOffset = end
	}
	return result, nil
}

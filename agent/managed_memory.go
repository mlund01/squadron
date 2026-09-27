package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"squadron/aitools"
	"squadron/config"
)

// ManagedMemoryStore is deliberately independent of mission memory. Its path
// contains both the managed-agent name and a hash of the authenticated owner;
// session mode adds a hash of the conversation ID. It exposes one "memory"
// slot to the agent's ordinary file tools.
type ManagedMemoryStore struct {
	mu            sync.Mutex
	root          string
	mode          string
	retentionDays int
}

type managedMemoryMetadata struct {
	LastUsedAt    time.Time `json:"last_used_at"`
	RetentionDays int       `json:"retention_days"`
}

// NewManagedMemoryStore returns nil for disabled memory. root is Squadron's
// state directory, not the workspace/config directory. The caller must pass
// the authenticated owner, rather than an agent-supplied value.
func NewManagedMemoryStore(root string, policy *config.AgentMemoryPolicy, owner, agentName, sessionID string) (aitools.MemoryStore, error) {
	if policy == nil || policy.Mode == "disabled" {
		return nil, nil
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if root == "" || owner == "" || agentName == "" {
		return nil, fmt.Errorf("managed memory requires a root, owner, and agent")
	}
	if strings.ContainsAny(agentName, `/\`) || agentName == "." || agentName == ".." {
		return nil, fmt.Errorf("invalid managed agent name")
	}
	base := filepath.Join(root, "memories", "managed", agentName, opaqueMemoryKey(owner))
	if policy.Mode == "session" {
		if sessionID == "" {
			return nil, fmt.Errorf("session memory requires a conversation ID")
		}
		base = filepath.Join(base, "sessions", opaqueMemoryKey(sessionID))
	} else {
		base = filepath.Join(base, "persistent")
	}
	store := &ManagedMemoryStore{root: base, mode: policy.Mode, retentionDays: policy.RetentionDays}
	if err := store.open(time.Now().UTC()); err != nil {
		return nil, err
	}
	return store, nil
}

func opaqueMemoryKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// open resets expired data and records the new access time. Only the exact
// owner/agent/session directory is ever removed; unrelated memory is untouched.
func (s *ManagedMemoryStore) open(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	metadataPath := filepath.Join(s.root, "metadata.json")
	if data, err := os.ReadFile(metadataPath); err == nil {
		var metadata managedMemoryMetadata
		if err := json.Unmarshal(data, &metadata); err != nil || metadata.LastUsedAt.IsZero() {
			return fmt.Errorf("managed memory metadata is invalid: %s", metadataPath)
		}
		if now.Sub(metadata.LastUsedAt) > time.Duration(s.retentionDays)*24*time.Hour {
			if err := os.RemoveAll(s.root); err != nil {
				return fmt.Errorf("expire managed memory: %w", err)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read managed memory metadata: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(s.root, "data"), 0700); err != nil {
		return fmt.Errorf("create managed memory: %w", err)
	}
	return s.touchLocked(now)
}

func (s *ManagedMemoryStore) touchLocked(now time.Time) error {
	data, err := json.Marshal(managedMemoryMetadata{LastUsedAt: now, RetentionDays: s.retentionDays})
	if err != nil {
		return err
	}
	path := filepath.Join(s.root, "metadata.json")
	tmp, err := os.CreateTemp(s.root, ".metadata-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (s *ManagedMemoryStore) ResolvePath(slotName, relPath string) (string, error) {
	if slotName != config.MemorySlotName {
		return "", fmt.Errorf("managed agent has no slot %q", slotName)
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("path escapes managed memory")
	}
	root := filepath.Join(s.root, "data")
	path := filepath.Join(root, relPath)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes managed memory")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.touchLocked(time.Now().UTC()); err != nil {
		return "", fmt.Errorf("update managed memory retention: %w", err)
	}
	return path, nil
}

func (s *ManagedMemoryStore) MemoryInfos() []aitools.MemoryInfo {
	kind := "managed_agent_persistent"
	if s.mode == "session" {
		kind = "managed_agent_session"
	}
	return []aitools.MemoryInfo{{Name: config.MemorySlotName, Description: "Managed agent file memory", Kind: kind}}
}

// SweepExpiredManagedMemory removes only scopes with valid metadata whose
// configured retention period has elapsed. A missing or corrupt metadata file
// is preserved for operator inspection rather than guessed to be disposable.
func SweepExpiredManagedMemory(root string) ([]string, error) {
	base := filepath.Join(root, "memories", "managed")
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var removed []string
	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "metadata.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var metadata managedMemoryMetadata
		if json.Unmarshal(data, &metadata) != nil || metadata.LastUsedAt.IsZero() || metadata.RetentionDays < 1 {
			return nil
		}
		if now.Sub(metadata.LastUsedAt) <= time.Duration(metadata.RetentionDays)*24*time.Hour {
			return nil
		}
		scope := filepath.Dir(path)
		if err := os.RemoveAll(scope); err != nil {
			return err
		}
		removed = append(removed, scope)
		return nil
	})
	return removed, err
}

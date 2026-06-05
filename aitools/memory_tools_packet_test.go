package aitools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubStore implements MemoryStore for tests.
type stubStore struct {
	root string
	slot string
}

func (s *stubStore) ResolvePath(slotName, relPath string) (string, error) {
	if slotName != s.slot {
		return "", fmt.Errorf("unknown slot %q", slotName)
	}
	cleaned := filepath.Clean(relPath)
	if cleaned == "." {
		return s.root, nil
	}
	full := filepath.Join(s.root, cleaned)
	if !strings.HasPrefix(full, s.root) {
		return "", fmt.Errorf("escape")
	}
	return full, nil
}

func (s *stubStore) MemoryInfos() []MemoryInfo {
	return []MemoryInfo{{Name: s.slot}}
}

func TestMemoryReadTool_RejectsBinaryForPacket(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "image.bin")
	if err := os.WriteFile(binPath, []byte{0x00, 0x01, 0x02, 0x03}, 0644); err != nil {
		t.Fatal(err)
	}

	store := &stubStore{root: dir, slot: PacketSlotPrefix + "ref"}
	tool := &MemoryReadTool{Store: store}

	params, _ := json.Marshal(map[string]any{
		"slot": store.slot,
		"path": "image.bin",
	})
	out := tool.Call(context.Background(), string(params))
	if !strings.Contains(out, "binary") {
		t.Fatalf("expected binary rejection, got: %s", out)
	}
}

func TestMemoryReadTool_AllowsTextForPacket(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# title\nbody"), 0644); err != nil {
		t.Fatal(err)
	}

	store := &stubStore{root: dir, slot: PacketSlotPrefix + "ref"}
	tool := &MemoryReadTool{Store: store}

	params, _ := json.Marshal(map[string]any{
		"slot": store.slot,
		"path": "notes.md",
	})
	out := tool.Call(context.Background(), string(params))
	if !strings.Contains(out, "# title") {
		t.Fatalf("expected text content, got: %s", out)
	}
}

func TestMemoryCreateTool_BlocksPacketWrites(t *testing.T) {
	dir := t.TempDir()
	store := &stubStore{root: dir, slot: PacketSlotPrefix + "ref"}
	tool := &MemoryCreateTool{Store: store}

	params, _ := json.Marshal(map[string]any{
		"slot":    store.slot,
		"path":    "new.txt",
		"content": "x",
	})
	out := tool.Call(context.Background(), string(params))
	if !strings.Contains(out, "read-only") {
		t.Fatalf("expected read-only rejection, got: %s", out)
	}
}

func TestMemoryDeleteTool_BlocksPacketDeletes(t *testing.T) {
	dir := t.TempDir()
	store := &stubStore{root: dir, slot: PacketSlotPrefix + "ref"}
	tool := &MemoryDeleteTool{Store: store}

	params, _ := json.Marshal(map[string]any{
		"slot": store.slot,
		"path": "anything.txt",
	})
	out := tool.Call(context.Background(), string(params))
	if !strings.Contains(out, "read-only") {
		t.Fatalf("expected read-only rejection, got: %s", out)
	}
}

func TestIsPacketSlot(t *testing.T) {
	if !IsPacketSlot("packet.foo") {
		t.Fatal("packet.foo should be detected as packet")
	}
	if IsPacketSlot("research") {
		t.Fatal("plain name should not be detected as packet")
	}
}

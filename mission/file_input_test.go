package mission

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"squadron/aitools"
	"squadron/config"
)

// fileInputRunner builds a Runner wired for file-input materialization: a
// project root (configPath), a mission declaring `inputs`, and raw values.
func fileInputRunner(t *testing.T, projectRoot string, inputs []config.MissionInput, raw map[string]string) *Runner {
	t.Helper()
	return &Runner{
		configPath: projectRoot,
		mission:    &config.Mission{Name: "m", Inputs: inputs},
		rawInputs:  raw,
	}
}

func envelope(t *testing.T, filename string, content []byte) string {
	t.Helper()
	b, err := json.Marshal(fileInputEnvelope{
		Filename:      filename,
		ContentBase64: base64.StdEncoding.EncodeToString(content),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMaterializeFileInputs_PathForm(t *testing.T) {
	withTempHome(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.md"), []byte("# Report\n"), 0644); err != nil {
		t.Fatal(err)
	}

	r := fileInputRunner(t, root,
		[]config.MissionInput{{Name: "doc", Type: config.InputTypeFile, Description: "the doc"}},
		map[string]string{"doc": "report.md"},
	)

	dirs, err := r.materializeFileInputs("run-1")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	slotDir, ok := dirs["doc"]
	if !ok {
		t.Fatal("doc not materialized")
	}
	got, err := os.ReadFile(filepath.Join(slotDir, "report.md"))
	if err != nil {
		t.Fatalf("read staged file: %v", err)
	}
	if string(got) != "# Report\n" {
		t.Fatalf("staged content = %q", got)
	}
}

func TestMaterializeFileInputs_Base64Form(t *testing.T) {
	withTempHome(t)
	r := fileInputRunner(t, t.TempDir(),
		[]config.MissionInput{{Name: "notes", Type: config.InputTypeFile}},
		map[string]string{"notes": envelope(t, "notes.txt", []byte("hello upload"))},
	)

	dirs, err := r.materializeFileInputs("run-1")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dirs["notes"], "notes.txt"))
	if err != nil {
		t.Fatalf("read staged file: %v", err)
	}
	if string(got) != "hello upload" {
		t.Fatalf("staged content = %q", got)
	}
}

func TestMaterializeFileInputs_Base64FilenameSanitized(t *testing.T) {
	withTempHome(t)
	r := fileInputRunner(t, t.TempDir(),
		[]config.MissionInput{{Name: "doc", Type: config.InputTypeFile}},
		// Hostile filename must collapse to a bare basename inside the slot.
		map[string]string{"doc": envelope(t, "../../etc/evil.txt", []byte("x"))},
	)

	dirs, err := r.materializeFileInputs("run-1")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	name, _ := existingStagedFile(dirs["doc"])
	if name != "evil.txt" {
		t.Fatalf("sanitized filename = %q, want evil.txt", name)
	}
	// And it must live inside the slot dir, not two levels up.
	if _, err := os.Stat(filepath.Join(dirs["doc"], "evil.txt")); err != nil {
		t.Fatalf("file not staged inside slot: %v", err)
	}
}

func TestMaterializeFileInputs_PathEscapeRejected(t *testing.T) {
	withTempHome(t)
	root := t.TempDir()
	// A sibling file outside the project root.
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })

	r := fileInputRunner(t, root,
		[]config.MissionInput{{Name: "doc", Type: config.InputTypeFile}},
		map[string]string{"doc": "../outside.txt"},
	)

	if _, err := r.materializeFileInputs("run-1"); err == nil {
		t.Fatal("expected path-escape rejection")
	} else if !strings.Contains(err.Error(), "outside the project root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMaterializeFileInputs_BadBase64Rejected(t *testing.T) {
	withTempHome(t)
	r := fileInputRunner(t, t.TempDir(),
		[]config.MissionInput{{Name: "doc", Type: config.InputTypeFile}},
		map[string]string{"doc": `{"filename":"x.txt","content_base64":"!!!not base64!!!"}`},
	)
	if _, err := r.materializeFileInputs("run-1"); err == nil {
		t.Fatal("expected base64 decode error")
	}
}

func TestMaterializeFileInputs_SizeCapRejected(t *testing.T) {
	withTempHome(t)
	big := make([]byte, maxFileInputBytes+1)
	r := fileInputRunner(t, t.TempDir(),
		[]config.MissionInput{{Name: "doc", Type: config.InputTypeFile}},
		map[string]string{"doc": envelope(t, "big.bin", big)},
	)
	if _, err := r.materializeFileInputs("run-1"); err == nil {
		t.Fatal("expected size-cap rejection")
	} else if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMaterializeFileInputs_MissingRequired(t *testing.T) {
	withTempHome(t)
	r := fileInputRunner(t, t.TempDir(),
		[]config.MissionInput{{Name: "doc", Type: config.InputTypeFile}},
		map[string]string{}, // nothing provided
	)
	if _, err := r.materializeFileInputs("run-1"); err == nil {
		t.Fatal("expected error for missing file input")
	}
}

// On resume the original source may be gone, but the staged copy persists —
// materialize must reuse it rather than re-read the source.
func TestMaterializeFileInputs_ReusesStagedOnResume(t *testing.T) {
	withTempHome(t)
	root := t.TempDir()

	r := fileInputRunner(t, root,
		[]config.MissionInput{{Name: "doc", Type: config.InputTypeFile}},
		map[string]string{"doc": envelope(t, "notes.txt", []byte("v1"))},
	)
	dirs1, err := r.materializeFileInputs("run-1")
	if err != nil {
		t.Fatalf("first materialize: %v", err)
	}

	// Second pass with NO raw value (simulating a resume where rawInputs were
	// dropped) must still resolve from the on-disk staged copy.
	r.rawInputs = map[string]string{}
	dirs2, err := r.materializeFileInputs("run-1")
	if err != nil {
		t.Fatalf("resume materialize: %v", err)
	}
	if dirs1["doc"] != dirs2["doc"] {
		t.Fatalf("slot dir changed across resume: %q vs %q", dirs1["doc"], dirs2["doc"])
	}
	got, _ := os.ReadFile(filepath.Join(dirs2["doc"], "notes.txt"))
	if string(got) != "v1" {
		t.Fatalf("staged content = %q", got)
	}
}

func TestBuildMemoryStoreWithFiles_ReadOnlyAndTextOnly(t *testing.T) {
	withTempHome(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte("body text"), 0644); err != nil {
		t.Fatal(err)
	}

	r := fileInputRunner(t, root,
		[]config.MissionInput{{Name: "doc", Type: config.InputTypeFile, Description: "the doc"}},
		map[string]string{"doc": "doc.md"},
	)
	dirs, err := r.materializeFileInputs("run-1")
	if err != nil {
		t.Fatal(err)
	}

	store, err := buildMemoryStoreWithFiles(r.mission, nil, nil, dirs, "run-1")
	if err != nil {
		t.Fatalf("build store: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}

	slot := config.InputFileSlotPrefix + "doc"
	ctx := context.Background()

	// Read works.
	read := &aitools.MemoryReadTool{Store: store}
	out := read.Call(ctx, fmt.Sprintf(`{"slot":%q,"path":"doc.md"}`, slot))
	if out != "body text" {
		t.Fatalf("file_read = %q", out)
	}

	// Create is rejected (read-only).
	create := &aitools.MemoryCreateTool{Store: store}
	out = create.Call(ctx, fmt.Sprintf(`{"slot":%q,"path":"new.txt","content":"x"}`, slot))
	if !strings.Contains(out, "read-only") {
		t.Fatalf("expected read-only rejection, got %q", out)
	}

	// Delete is rejected (read-only).
	del := &aitools.MemoryDeleteTool{Store: store}
	out = del.Call(ctx, fmt.Sprintf(`{"slot":%q,"path":"doc.md"}`, slot))
	if !strings.Contains(out, "read-only") {
		t.Fatalf("expected read-only rejection, got %q", out)
	}

	// Binary content is rejected on read (text-only).
	if err := os.WriteFile(filepath.Join(dirs["doc"], "blob.bin"), []byte{0x00, 0x01, 0x02}, 0644); err != nil {
		t.Fatal(err)
	}
	out = read.Call(ctx, fmt.Sprintf(`{"slot":%q,"path":"blob.bin"}`, slot))
	if !strings.Contains(out, "UTF-8 text only") {
		t.Fatalf("expected binary rejection, got %q", out)
	}

	// Slot is surfaced in MemoryInfos with the user's description.
	var found bool
	for _, info := range store.MemoryInfos() {
		if info.Name == slot {
			found = true
			if info.Description != "the doc" {
				t.Fatalf("description = %q", info.Description)
			}
		}
	}
	if !found {
		t.Fatal("file-input slot missing from MemoryInfos")
	}
}

func TestSweepExpiredInputs(t *testing.T) {
	home := withTempHome(t)

	runDir := filepath.Join(home, inputsSubdir, "m", "old-run")
	if err := os.MkdirAll(filepath.Join(runDir, "doc"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "doc", "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// Sidecar dated well past the cleanup window.
	meta := runMetadata{
		Mission:     "m",
		MissionID:   "old-run",
		CreatedAt:   time.Now().UTC().Add(-time.Duration(config.ScratchpadCleanupDays+1) * 24 * time.Hour),
		CleanupDays: config.ScratchpadCleanupDays,
	}
	b, _ := json.MarshalIndent(&meta, "", "  ")
	if err := os.WriteFile(filepath.Join(runDir, runMetadataFile), b, 0644); err != nil {
		t.Fatal(err)
	}

	removed, err := SweepExpiredInputs()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(removed) != 1 {
		t.Fatalf("expected 1 removed, got %d (%v)", len(removed), removed)
	}
	if _, err := os.Stat(runDir); !os.IsNotExist(err) {
		t.Fatalf("expected run dir removed, stat err = %v", err)
	}
}

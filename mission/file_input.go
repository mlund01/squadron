package mission

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"squadron/config"
	"squadron/internal/paths"
)

// maxFileInputBytes caps the size of a single materialized file input — both
// the decoded base64 payload and a copied-in path. base64 inflates ~33%, so a
// 10 MB ceiling means up to ~13 MB of envelope text travels through the input
// plumbing (and the persisted run record on resume).
const maxFileInputBytes = 10 * 1024 * 1024

// fileInputEnvelope is the base64 upload form of a file input value, used when
// the caller has no path on the Squadron host (command center, webhooks, MCP
// clients):
//
//	{"filename": "report.md", "content_base64": "IyBSZXBvcnQK..."}
type fileInputEnvelope struct {
	Filename      string `json:"filename"`
	ContentBase64 string `json:"content_base64"`
}

// materializeFileInputs stages every file-typed mission input into an isolated
// per-input directory under the run's inputs staging dir and returns a map of
// input name -> that directory (which holds exactly the one staged file). Both
// supported value forms converge here: a project-relative path (copied in) and
// a base64 envelope (decoded in).
//
// It returns nil when the mission declares no file inputs. A directory already
// holding a staged file is reused as-is — this is what makes resume robust
// (the original source path may be long gone) and re-entrant.
func (r *Runner) materializeFileInputs(missionInstanceID string) (map[string]string, error) {
	var fileInputs []config.MissionInput
	for _, in := range r.mission.Inputs {
		if in.Type == config.InputTypeFile {
			fileInputs = append(fileInputs, in)
		}
	}
	if len(fileInputs) == 0 {
		return nil, nil
	}

	runDir, err := MissionInputsPath(r.mission.Name, missionInstanceID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return nil, fmt.Errorf("create inputs dir: %w", err)
	}
	// Sidecar drives the cleanup sweep, exactly like scratchpads.
	if err := writeRunMetadata(runDir, r.mission.Name, missionInstanceID, config.ScratchpadCleanupDays); err != nil {
		return nil, fmt.Errorf("write inputs metadata: %w", err)
	}

	dirs := make(map[string]string, len(fileInputs))
	for _, in := range fileInputs {
		slotDir := filepath.Join(runDir, in.Name)

		// Already staged (resume / retry) — reuse without touching the source.
		if name, _ := existingStagedFile(slotDir); name != "" {
			dirs[in.Name] = slotDir
			continue
		}

		raw, ok := r.rawInputs[in.Name]
		if !ok || strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("file input %q: no value provided", in.Name)
		}
		if err := os.MkdirAll(slotDir, 0755); err != nil {
			return nil, fmt.Errorf("file input %q: create dir: %w", in.Name, err)
		}
		if err := r.stageFileInput(slotDir, raw); err != nil {
			return nil, fmt.Errorf("file input %q: %w", in.Name, err)
		}
		dirs[in.Name] = slotDir
	}
	return dirs, nil
}

// stageFileInput writes the one file for a single input into slotDir, choosing
// the base64-envelope or path form based on the raw value.
func (r *Runner) stageFileInput(slotDir, raw string) error {
	if env, ok := parseFileEnvelope(raw); ok {
		return stageBase64Input(slotDir, env)
	}
	return r.stagePathInput(slotDir, raw)
}

// parseFileEnvelope recognizes the base64 upload form: a JSON object carrying a
// non-empty content_base64. Anything else is treated as a path.
func parseFileEnvelope(raw string) (fileInputEnvelope, bool) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "{") {
		return fileInputEnvelope{}, false
	}
	var env fileInputEnvelope
	if err := json.Unmarshal([]byte(s), &env); err != nil {
		return fileInputEnvelope{}, false
	}
	if env.ContentBase64 == "" {
		return fileInputEnvelope{}, false
	}
	return env, true
}

// stageBase64Input decodes an upload envelope and writes it under a sanitized
// filename. The filename is attacker-controlled in an upload scenario, so it is
// reduced to a bare basename before it touches the filesystem.
func stageBase64Input(slotDir string, env fileInputEnvelope) error {
	name := sanitizeFilename(env.Filename)
	if name == "" {
		return fmt.Errorf("base64 upload requires a valid filename")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(env.ContentBase64))
	if err != nil {
		return fmt.Errorf("invalid base64 content: %w", err)
	}
	if len(data) > maxFileInputBytes {
		return fmt.Errorf("file too large (%d bytes, max %d)", len(data), maxFileInputBytes)
	}
	return os.WriteFile(filepath.Join(slotDir, name), data, 0644)
}

// stagePathInput resolves a project-relative path, validates it, and copies the
// file into slotDir. Paths must stay within the project root (r.configPath);
// files outside the project must be supplied via the base64 upload form. This
// bounds what a semi-trusted remote caller (MCP / webhook) can ask Squadron to
// read off the host.
func (r *Runner) stagePathInput(slotDir, rawPath string) error {
	src, err := resolveProjectFilePath(r.configPath, rawPath)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("cannot access file: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory, not a file: %s", rawPath)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", rawPath)
	}
	if info.Size() > maxFileInputBytes {
		return fmt.Errorf("file too large (%d bytes, max %d)", info.Size(), maxFileInputBytes)
	}
	return copyFile(src, filepath.Join(slotDir, filepath.Base(src)))
}

// resolveProjectFilePath anchors rawPath at the project root and rejects any
// result that escapes it (absolute-outside or ".." traversal).
func resolveProjectFilePath(projectRoot, rawPath string) (string, error) {
	if strings.TrimSpace(rawPath) == "" {
		return "", fmt.Errorf("path is empty")
	}
	root := projectRoot
	if root == "" {
		// No -c root (e.g. some test paths): anchor at CWD so behavior is
		// still deterministic rather than panicking on an empty join base.
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		root = cwd
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean(rawPath)
	abs := cleaned
	if !filepath.IsAbs(cleaned) {
		abs = filepath.Join(root, cleaned)
	}
	if !paths.IsInside(root, abs, false) {
		return "", fmt.Errorf("path %q is outside the project root %q; supply files outside the project via the base64 upload form", rawPath, root)
	}
	return abs, nil
}

// sanitizeFilename reduces an arbitrary, possibly-hostile name to a safe bare
// basename, or "" if nothing usable remains.
func sanitizeFilename(name string) string {
	base := filepath.Base(filepath.Clean(strings.TrimSpace(name)))
	switch base {
	case ".", "..", string(filepath.Separator), "":
		return ""
	}
	return base
}

// existingStagedFile returns the name of the first regular file in dir, or ""
// if the directory is absent/empty. Used to detect an already-materialized
// input on resume.
func existingStagedFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			return e.Name(), nil
		}
	}
	return "", nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

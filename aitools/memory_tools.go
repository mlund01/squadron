package aitools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Reserved slot names for the mission-scoped storage slots. Agents address
// them via the `slot` parameter on the file tools (alongside any shared
// memory names declared at the top level).
const (
	MemorySlotName     = "memory"
	ScratchpadSlotName = "scratchpad"
)

// ContextSlotPrefix marks a slot as belonging to a read-only context bundle.
// The MemoryStore doesn't distinguish read-only from read-write itself; the
// file tools below enforce the policy by inspecting the slot name with
// IsContextSlot. Mirrored in config.ContextSlotPrefix — both must stay in
// sync.
const ContextSlotPrefix = "context."

// IsContextSlot reports whether a slot name belongs to a context bundle
// (read-only, text-only).
func IsContextSlot(name string) bool {
	return strings.HasPrefix(name, ContextSlotPrefix)
}

// looksBinary returns true if the sample contains a NUL byte in its first
// 8 KB — a cheap "this isn't UTF-8 text" proxy. False positives on
// UTF-16/UTF-32 text are intentional given the file-tools' UTF-8-only
// surface (callers see a clear error rather than gibberish output).
func looksBinary(content []byte) bool {
	limit := len(content)
	if limit > 8192 {
		limit = 8192
	}
	for i := 0; i < limit; i++ {
		if content[i] == 0 {
			return true
		}
	}
	return false
}

// MemoryStore provides slot access for missions. Implementations resolve a
// slot name (the mission's "memory" / "scratchpad", or a shared memory's
// label) to an absolute path. Every slot is read+write — there is no
// read-only mode at this layer.
type MemoryStore interface {
	// ResolvePath resolves a slot name + relative path to an absolute path.
	ResolvePath(slotName string, relPath string) (string, error)
	// MemoryInfos returns info about all available slots.
	MemoryInfos() []MemoryInfo
}

// MemoryInfo describes an available slot.
type MemoryInfo struct {
	Name        string
	Description string
}

// validateRelPath ensures a path is relative and doesn't escape the slot root.
func validateRelPath(relPath string) error {
	if relPath == "" {
		return fmt.Errorf("path is required")
	}
	cleaned := filepath.Clean(relPath)
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") ||
		strings.Contains(cleaned, string(filepath.Separator)+"..") {
		return fmt.Errorf("invalid path: must be relative and within the slot root")
	}
	return nil
}

// resolveSlotPath is a helper that resolves the slot and validates the
// relative path.
func resolveSlotPath(store MemoryStore, name, relPath string) (string, error) {
	if err := validateRelPath(relPath); err != nil {
		return "", err
	}
	return store.ResolvePath(name, relPath)
}

// slotParamDescription is reused across every file tool's `slot` parameter
// so the agent sees a consistent description.
const slotParamDescription = "Slot to operate in. Use \"memory\" for the mission's persistent memory, \"scratchpad\" for its ephemeral per-run scratchpad, or a shared memory name."

// =============================================================================
// file_list — List files and directories
// =============================================================================

type MemoryListTool struct {
	Store MemoryStore
}

func (t *MemoryListTool) ToolName() string { return "file_list" }

func (t *MemoryListTool) ToolDescription() string {
	return "List files and directories in a slot. Returns names, types (file/dir), and sizes. Results are paginated (default 100 per page). Use 'offset' to get subsequent pages."
}

func (t *MemoryListTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"slot": {
				Type:        TypeString,
				Description: slotParamDescription,
			},
			"path": {
				Type:        TypeString,
				Description: "Relative subdirectory path within the slot. Omit to list the root.",
			},
			"recursive": {
				Type:        TypeBoolean,
				Description: "If true, list all files recursively with full relative paths.",
			},
			"limit": {
				Type:        TypeInteger,
				Description: "Max entries to return. Default 100.",
			},
			"offset": {
				Type:        TypeInteger,
				Description: "Number of entries to skip (for pagination). Default 0.",
			},
		},
		Required: []string{"slot"},
	}
}

type memoryListParams struct {
	Slot      string `json:"slot"`
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}

const defaultListLimit = 100

func (t *MemoryListTool) Call(ctx context.Context, params string) string {
	var p memoryListParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.Limit <= 0 {
		p.Limit = defaultListLimit
	}

	// Resolve base directory
	var absPath string
	var err error
	if p.Path == "" {
		absPath, err = t.Store.ResolvePath(p.Slot, ".")
	} else {
		absPath, err = resolveSlotPath(t.Store, p.Slot, p.Path)
	}
	if err != nil {
		return "Error: " + err.Error()
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return "Error: " + err.Error()
	}
	if !info.IsDir() {
		return "Error: path is not a directory"
	}

	var allEntries []string
	if p.Recursive {
		allEntries = collectRecursive(absPath)
	} else {
		allEntries = collectFlat(absPath)
	}

	total := len(allEntries)
	if total == 0 {
		return "(empty directory)"
	}

	// Apply pagination
	start := p.Offset
	if start >= total {
		return fmt.Sprintf("(no entries at offset %d — total: %d)", start, total)
	}
	end := start + p.Limit
	if end > total {
		end = total
	}
	page := allEntries[start:end]

	var sb strings.Builder
	for _, entry := range page {
		sb.WriteString(entry)
		sb.WriteByte('\n')
	}

	// Pagination footer
	remaining := total - end
	fmt.Fprintf(&sb, "\n--- Showing %d-%d of %d entries", start+1, end, total)
	if remaining > 0 {
		fmt.Fprintf(&sb, " (%d more — use offset: %d to continue)", remaining, end)
	}
	fmt.Fprintf(&sb, " ---")

	return sb.String()
}

func collectFlat(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var result []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if entry.IsDir() {
			result = append(result, fmt.Sprintf("[dir]  %s/", entry.Name()))
		} else {
			result = append(result, fmt.Sprintf("[file] %s (%s)", entry.Name(), formatSize(info.Size())))
		}
	}
	return result
}

func collectRecursive(root string) []string {
	var result []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if d.IsDir() {
			result = append(result, fmt.Sprintf("[dir]  %s/", rel))
		} else {
			result = append(result, fmt.Sprintf("[file] %s (%s)", rel, formatSize(info.Size())))
		}
		return nil
	})
	return result
}

func formatSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	if bytes < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(bytes)/(1024*1024*1024))
}

// =============================================================================
// file_read — Read file content
// =============================================================================

type MemoryReadTool struct {
	Store MemoryStore
}

func (t *MemoryReadTool) ToolName() string { return "file_read" }

func (t *MemoryReadTool) ToolDescription() string {
	return "Read the contents of a file in a slot. Optionally limit to the first N lines or N bytes."
}

func (t *MemoryReadTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"slot": {
				Type:        TypeString,
				Description: slotParamDescription,
			},
			"path": {
				Type:        TypeString,
				Description: "Relative file path within the slot.",
			},
			"max_lines": {
				Type:        TypeInteger,
				Description: "Return only the first N lines. 0 or omit for no limit.",
			},
			"max_bytes": {
				Type:        TypeInteger,
				Description: "Return only the first N bytes. 0 or omit for no limit.",
			},
		},
		Required: []string{"slot", "path"},
	}
}

type memoryReadParams struct {
	Slot     string `json:"slot"`
	Path     string `json:"path"`
	MaxLines int    `json:"max_lines"`
	MaxBytes int    `json:"max_bytes"`
}

const maxReadSize = 10 * 1024 * 1024 // 10MB

func (t *MemoryReadTool) Call(ctx context.Context, params string) string {
	var p memoryReadParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.Path == "" {
		return "Error: path is required"
	}

	absPath, err := resolveSlotPath(t.Store, p.Slot, p.Path)
	if err != nil {
		return "Error: " + err.Error()
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return "Error: " + err.Error()
	}
	if info.IsDir() {
		return "Error: path is a directory, not a file"
	}
	if info.Size() > maxReadSize {
		return fmt.Sprintf("Error: file too large (%s). Use max_bytes to read a portion.", formatSize(info.Size()))
	}

	f, err := os.Open(absPath)
	if err != nil {
		return "Error: " + err.Error()
	}
	defer f.Close()

	var content []byte
	if p.MaxBytes > 0 {
		content = make([]byte, p.MaxBytes)
		n, err := f.Read(content)
		if err != nil && err != io.EOF {
			return "Error: " + err.Error()
		}
		content = content[:n]
	} else {
		content, err = io.ReadAll(f)
		if err != nil {
			return "Error: " + err.Error()
		}
	}

	// Contexts are read-only reference data and may only hold text-readable
	// files. Reject binary payloads up front so the LLM doesn't see a wall
	// of garbled bytes.
	if IsContextSlot(p.Slot) && looksBinary(content) {
		return "Error: file appears to be binary or non-UTF-8 encoded; context slots accept UTF-8 text only"
	}

	text := string(content)

	if p.MaxLines > 0 {
		lines := strings.SplitN(text, "\n", p.MaxLines+1)
		if len(lines) > p.MaxLines {
			lines = lines[:p.MaxLines]
		}
		text = strings.Join(lines, "\n")
	}

	return text
}

// =============================================================================
// file_create — Create or write to a file
// =============================================================================

type MemoryCreateTool struct {
	Store MemoryStore
}

func (t *MemoryCreateTool) ToolName() string { return "file_create" }

func (t *MemoryCreateTool) ToolDescription() string {
	return "Create or write to a file in a slot. By default, creates a new file (fails if it already exists). Use 'overwrite' to replace an existing file, or 'append' to add to an existing file."
}

func (t *MemoryCreateTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"slot": {
				Type:        TypeString,
				Description: slotParamDescription,
			},
			"path": {
				Type:        TypeString,
				Description: "Relative file path within the slot.",
			},
			"content": {
				Type:        TypeString,
				Description: "Content to write to the file.",
			},
			"append": {
				Type:        TypeBoolean,
				Description: "If true, append content to an existing file (fails if file doesn't exist). Overrides 'overwrite'.",
			},
			"overwrite": {
				Type:        TypeBoolean,
				Description: "If true, overwrite the file if it already exists. Ignored when 'append' is true.",
			},
		},
		Required: []string{"slot", "path", "content"},
	}
}

type memoryCreateParams struct {
	Slot      string `json:"slot"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Append    bool   `json:"append"`
	Overwrite bool   `json:"overwrite"`
}

func (t *MemoryCreateTool) Call(ctx context.Context, params string) string {
	var p memoryCreateParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.Path == "" {
		return "Error: path is required"
	}

	if IsContextSlot(p.Slot) {
		return "Error: slot is read-only (context bundles are immutable)"
	}

	absPath, err := resolveSlotPath(t.Store, p.Slot, p.Path)
	if err != nil {
		return "Error: " + err.Error()
	}

	if p.Append {
		// Append mode: file must exist
		f, err := os.OpenFile(absPath, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return "Error: " + err.Error()
		}
		defer f.Close()
		if _, err := f.WriteString(p.Content); err != nil {
			return "Error: " + err.Error()
		}
		return "Content appended successfully."
	}

	if !p.Overwrite {
		// Create mode: must not already exist
		if _, err := os.Stat(absPath); err == nil {
			return "Error: file already exists. Use 'overwrite: true' to replace or 'append: true' to add to it."
		}
	}

	// Create parent directories if needed
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "Error: creating directories - " + err.Error()
	}

	if err := os.WriteFile(absPath, []byte(p.Content), 0644); err != nil {
		return "Error: " + err.Error()
	}

	return "File created successfully."
}

// =============================================================================
// file_delete — Delete a file
// =============================================================================

type MemoryDeleteTool struct {
	Store MemoryStore
}

func (t *MemoryDeleteTool) ToolName() string { return "file_delete" }

func (t *MemoryDeleteTool) ToolDescription() string {
	return "Delete a file in a slot. Only files can be deleted, not directories."
}

func (t *MemoryDeleteTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"slot": {
				Type:        TypeString,
				Description: slotParamDescription,
			},
			"path": {
				Type:        TypeString,
				Description: "Relative file path within the slot.",
			},
		},
		Required: []string{"slot", "path"},
	}
}

type memoryDeleteParams struct {
	Slot string `json:"slot"`
	Path   string `json:"path"`
}

func (t *MemoryDeleteTool) Call(ctx context.Context, params string) string {
	var p memoryDeleteParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.Path == "" {
		return "Error: path is required"
	}

	if IsContextSlot(p.Slot) {
		return "Error: slot is read-only (context bundles are immutable)"
	}

	absPath, err := resolveSlotPath(t.Store, p.Slot, p.Path)
	if err != nil {
		return "Error: " + err.Error()
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return "Error: " + err.Error()
	}
	if info.IsDir() {
		return "Error: cannot delete directories, only files"
	}

	if err := os.Remove(absPath); err != nil {
		return "Error: " + err.Error()
	}

	return "File deleted successfully."
}

// =============================================================================
// file_search — Search for files by name pattern
// =============================================================================

type MemorySearchTool struct {
	Store MemoryStore
}

func (t *MemorySearchTool) ToolName() string { return "file_search" }

func (t *MemorySearchTool) ToolDescription() string {
	return "Search for files by name within a slot using a regex pattern. Returns matching file paths with sizes. Searches recursively by default. Results are paginated (default 50)."
}

func (t *MemorySearchTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"slot": {
				Type:        TypeString,
				Description: slotParamDescription,
			},
			"path": {
				Type:        TypeString,
				Description: "Relative path to search within. Omit to search the slot root.",
			},
			"pattern": {
				Type:        TypeString,
				Description: "Regex pattern to match against file names (not full paths). Example: '\\.go$' to find Go files.",
			},
			"limit": {
				Type:        TypeInteger,
				Description: "Max results to return. Default 50.",
			},
			"offset": {
				Type:        TypeInteger,
				Description: "Number of results to skip (for pagination). Default 0.",
			},
		},
		Required: []string{"slot", "pattern"},
	}
}

type memorySearchParams struct {
	Slot    string `json:"slot"`
	Path    string `json:"path"`
	Pattern string `json:"pattern"`
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
}

const defaultSearchLimit = 50

func (t *MemorySearchTool) Call(ctx context.Context, params string) string {
	var p memorySearchParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.Pattern == "" {
		return "Error: pattern is required"
	}

	re, err := regexp.Compile(p.Pattern)
	if err != nil {
		return "Error: invalid regex pattern - " + err.Error()
	}

	if p.Limit <= 0 {
		p.Limit = defaultSearchLimit
	}

	// Resolve search root
	var absPath string
	if p.Path == "" {
		absPath, err = t.Store.ResolvePath(p.Slot, ".")
	} else {
		absPath, err = resolveSlotPath(t.Store, p.Slot, p.Path)
	}
	if err != nil {
		return "Error: " + err.Error()
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return "Error: " + err.Error()
	}
	if !info.IsDir() {
		return "Error: path is not a directory"
	}

	// Walk recursively and collect filename matches
	var results []string
	filepath.WalkDir(absPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if re.MatchString(d.Name()) {
			rel, _ := filepath.Rel(absPath, path)
			info, err := d.Info()
			if err != nil {
				return nil
			}
			results = append(results, fmt.Sprintf("[file] %s (%s)", rel, formatSize(info.Size())))
		}
		return nil
	})

	total := len(results)
	if total == 0 {
		return "No files found matching pattern."
	}

	// Apply pagination
	start := p.Offset
	if start >= total {
		return fmt.Sprintf("(no results at offset %d — total: %d)", start, total)
	}
	end := start + p.Limit
	if end > total {
		end = total
	}
	page := results[start:end]

	var sb strings.Builder
	for _, entry := range page {
		sb.WriteString(entry)
		sb.WriteByte('\n')
	}

	remaining := total - end
	fmt.Fprintf(&sb, "\n--- %d-%d of %d files", start+1, end, total)
	if remaining > 0 {
		fmt.Fprintf(&sb, " (%d more — use offset: %d to continue)", remaining, end)
	}
	fmt.Fprintf(&sb, " ---")

	return sb.String()
}

// =============================================================================
// file_grep — Search file contents with regex
// =============================================================================

type MemoryGrepTool struct {
	Store MemoryStore
}

func (t *MemoryGrepTool) ToolName() string { return "file_grep" }

func (t *MemoryGrepTool) ToolDescription() string {
	return "Search file contents within a slot using a regex pattern. Returns matching lines with file paths and line numbers. Results are paginated (default 50 matches)."
}

func (t *MemoryGrepTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"slot": {
				Type:        TypeString,
				Description: slotParamDescription,
			},
			"path": {
				Type:        TypeString,
				Description: "Relative path to search within. Omit to search the slot root.",
			},
			"pattern": {
				Type:        TypeString,
				Description: "Regex pattern to search for in file contents.",
			},
			"recursive": {
				Type:        TypeBoolean,
				Description: "If true, search files in subdirectories recursively. Default false.",
			},
			"limit": {
				Type:        TypeInteger,
				Description: "Max matches to return. Default 50.",
			},
			"offset": {
				Type:        TypeInteger,
				Description: "Number of matches to skip (for pagination). Default 0.",
			},
		},
		Required: []string{"slot", "pattern"},
	}
}

type memoryGrepParams struct {
	Slot      string `json:"slot"`
	Path      string `json:"path"`
	Pattern   string `json:"pattern"`
	Recursive bool   `json:"recursive"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}

const defaultGrepLimit = 50

func (t *MemoryGrepTool) Call(ctx context.Context, params string) string {
	var p memoryGrepParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.Pattern == "" {
		return "Error: pattern is required"
	}

	re, err := regexp.Compile(p.Pattern)
	if err != nil {
		return "Error: invalid regex pattern - " + err.Error()
	}

	if p.Limit <= 0 {
		p.Limit = defaultGrepLimit
	}

	// Resolve search root
	var absPath string
	if p.Path == "" {
		absPath, err = t.Store.ResolvePath(p.Slot, ".")
	} else {
		absPath, err = resolveSlotPath(t.Store, p.Slot, p.Path)
	}
	if err != nil {
		return "Error: " + err.Error()
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return "Error: " + err.Error()
	}
	if !info.IsDir() {
		return "Error: path is not a directory"
	}

	// Collect all matches
	type match struct {
		file string
		line int
		text string
	}
	var matches []match

	isContext := IsContextSlot(p.Slot)
	grepFile := func(filePath string, relPath string) {
		f, err := os.Open(filePath)
		if err != nil {
			return
		}
		defer f.Close()

		// Skip binary files in context slots so grep doesn't pollute output
		// with garbage matches from images/archives/etc. Use io.ReadFull so
		// short reads on slow FS still get up to 8KB before we judge.
		if isContext {
			head := make([]byte, 8192)
			n, err := io.ReadFull(f, head)
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
				return
			}
			if looksBinary(head[:n]) {
				return
			}
			if _, err := f.Seek(0, 0); err != nil {
				return
			}
		}

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				matches = append(matches, match{file: relPath, line: lineNum, text: line})
			}
		}
	}

	if p.Recursive {
		filepath.WalkDir(absPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(absPath, path)
			grepFile(path, rel)
			return nil
		})
	} else {
		entries, err := os.ReadDir(absPath)
		if err != nil {
			return "Error: " + err.Error()
		}
		for _, entry := range entries {
			if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			filePath := filepath.Join(absPath, entry.Name())
			grepFile(filePath, entry.Name())
		}
	}

	total := len(matches)
	if total == 0 {
		return "No matches found."
	}

	// Apply pagination
	start := p.Offset
	if start >= total {
		return fmt.Sprintf("(no matches at offset %d — total: %d)", start, total)
	}
	end := start + p.Limit
	if end > total {
		end = total
	}
	page := matches[start:end]

	var sb strings.Builder
	for _, m := range page {
		text := m.text
		if len(text) > 200 {
			text = text[:200] + "..."
		}
		fmt.Fprintf(&sb, "%s:%d: %s\n", m.file, m.line, strings.TrimSpace(text))
	}

	remaining := total - end
	fmt.Fprintf(&sb, "\n--- %d-%d of %d matches", start+1, end, total)
	if remaining > 0 {
		fmt.Fprintf(&sb, " (%d more — use offset: %d to continue)", remaining, end)
	}
	fmt.Fprintf(&sb, " ---")

	return sb.String()
}

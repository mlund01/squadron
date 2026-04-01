package aitools

import (
	"context"
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FolderStore provides folder access for missions.
// Implementations resolve folder names to paths and enforce security.
type FolderStore interface {
	// ResolvePath resolves a folder name + relative path to an absolute path.
	// Empty folderName = dedicated mission folder.
	// Returns: absolute path, writable flag, error.
	ResolvePath(folderName string, relPath string) (string, bool, error)
	// DefaultFolder returns the name of the dedicated folder ("" if none).
	DefaultFolder() string
	// FolderInfos returns info about all available folders.
	FolderInfos() []FolderInfo
}

// FolderInfo describes an available folder.
type FolderInfo struct {
	Name        string
	Description string
	Writable    bool
	IsDedicated bool // true for mission's own folder
}

// validateRelPath ensures a path is relative and doesn't escape the folder root.
func validateRelPath(relPath string) error {
	if relPath == "" {
		return fmt.Errorf("path is required")
	}
	cleaned := filepath.Clean(relPath)
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") ||
		strings.Contains(cleaned, string(filepath.Separator)+"..") {
		return fmt.Errorf("invalid path: must be relative and within folder")
	}
	return nil
}

// resolveFolderPath is a helper that resolves the folder and validates the path.
func resolveFolderPath(store FolderStore, folderName, relPath string) (string, bool, error) {
	if err := validateRelPath(relPath); err != nil {
		return "", false, err
	}
	return store.ResolvePath(folderName, relPath)
}

// =============================================================================
// folder_list — List files and directories
// =============================================================================

type FolderListTool struct {
	Store FolderStore
}

func (t *FolderListTool) ToolName() string { return "file_list" }

func (t *FolderListTool) ToolDescription() string {
	return "List files and directories in a folder. Returns names, types (file/dir), and sizes. Results are paginated (default 100 per page). Use 'offset' to get subsequent pages."
}

func (t *FolderListTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"folder": {
				Type:        TypeString,
				Description: "Folder name. Omit to use the default mission folder.",
			},
			"path": {
				Type:        TypeString,
				Description: "Relative subdirectory path within the folder. Omit to list the root.",
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
	}
}

type folderListParams struct {
	Folder    string `json:"folder"`
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}

const defaultListLimit = 100

func (t *FolderListTool) Call(ctx context.Context, params string) string {
	var p folderListParams
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
		absPath, _, err = t.Store.ResolvePath(p.Folder, ".")
	} else {
		absPath, _, err = resolveFolderPath(t.Store, p.Folder, p.Path)
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
// folder_read — Read file content
// =============================================================================

type FolderReadTool struct {
	Store FolderStore
}

func (t *FolderReadTool) ToolName() string { return "file_read" }

func (t *FolderReadTool) ToolDescription() string {
	return "Read the contents of a file in a folder. Optionally limit to the first N lines or N bytes."
}

func (t *FolderReadTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"folder": {
				Type:        TypeString,
				Description: "Folder name. Omit to use the default mission folder.",
			},
			"path": {
				Type:        TypeString,
				Description: "Relative file path within the folder.",
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
		Required: []string{"path"},
	}
}

type folderReadParams struct {
	Folder   string `json:"folder"`
	Path     string `json:"path"`
	MaxLines int    `json:"max_lines"`
	MaxBytes int    `json:"max_bytes"`
}

const maxReadSize = 10 * 1024 * 1024 // 10MB

func (t *FolderReadTool) Call(ctx context.Context, params string) string {
	var p folderReadParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.Path == "" {
		return "Error: path is required"
	}

	absPath, _, err := resolveFolderPath(t.Store, p.Folder, p.Path)
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
// folder_create — Create or write to a file
// =============================================================================

type FolderCreateTool struct {
	Store FolderStore
}

func (t *FolderCreateTool) ToolName() string { return "file_create" }

func (t *FolderCreateTool) ToolDescription() string {
	return "Create or write to a file in a folder. By default, creates a new file (fails if it already exists). Use 'overwrite' to replace an existing file, or 'append' to add to an existing file."
}

func (t *FolderCreateTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"folder": {
				Type:        TypeString,
				Description: "Folder name. Omit to use the default mission folder.",
			},
			"path": {
				Type:        TypeString,
				Description: "Relative file path within the folder.",
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
		Required: []string{"path", "content"},
	}
}

type folderCreateParams struct {
	Folder    string `json:"folder"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Append    bool   `json:"append"`
	Overwrite bool   `json:"overwrite"`
}

func (t *FolderCreateTool) Call(ctx context.Context, params string) string {
	var p folderCreateParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.Path == "" {
		return "Error: path is required"
	}

	absPath, writable, err := resolveFolderPath(t.Store, p.Folder, p.Path)
	if err != nil {
		return "Error: " + err.Error()
	}

	if !writable {
		return "Error: folder is read-only"
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
// folder_delete — Delete a file
// =============================================================================

type FolderDeleteTool struct {
	Store FolderStore
}

func (t *FolderDeleteTool) ToolName() string { return "file_delete" }

func (t *FolderDeleteTool) ToolDescription() string {
	return "Delete a file in a folder. Only files can be deleted, not directories."
}

func (t *FolderDeleteTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"folder": {
				Type:        TypeString,
				Description: "Folder name. Omit to use the default mission folder.",
			},
			"path": {
				Type:        TypeString,
				Description: "Relative file path within the folder.",
			},
		},
		Required: []string{"path"},
	}
}

type folderDeleteParams struct {
	Folder string `json:"folder"`
	Path   string `json:"path"`
}

func (t *FolderDeleteTool) Call(ctx context.Context, params string) string {
	var p folderDeleteParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.Path == "" {
		return "Error: path is required"
	}

	absPath, writable, err := resolveFolderPath(t.Store, p.Folder, p.Path)
	if err != nil {
		return "Error: " + err.Error()
	}

	if !writable {
		return "Error: folder is read-only"
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

type FolderSearchTool struct {
	Store FolderStore
}

func (t *FolderSearchTool) ToolName() string { return "file_search" }

func (t *FolderSearchTool) ToolDescription() string {
	return "Search for files by name using a regex pattern. Returns matching file paths with sizes. Searches recursively by default. Results are paginated (default 50)."
}

func (t *FolderSearchTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"folder": {
				Type:        TypeString,
				Description: "Folder name. Omit to use the default mission folder.",
			},
			"path": {
				Type:        TypeString,
				Description: "Relative path to search within. Omit to search the folder root.",
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
		Required: []string{"pattern"},
	}
}

type folderSearchParams struct {
	Folder  string `json:"folder"`
	Path    string `json:"path"`
	Pattern string `json:"pattern"`
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
}

const defaultSearchLimit = 50

func (t *FolderSearchTool) Call(ctx context.Context, params string) string {
	var p folderSearchParams
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
		absPath, _, err = t.Store.ResolvePath(p.Folder, ".")
	} else {
		absPath, _, err = resolveFolderPath(t.Store, p.Folder, p.Path)
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

type FolderGrepTool struct {
	Store FolderStore
}

func (t *FolderGrepTool) ToolName() string { return "file_grep" }

func (t *FolderGrepTool) ToolDescription() string {
	return "Search file contents using a regex pattern. Returns matching lines with file paths and line numbers. Results are paginated (default 50 matches)."
}

func (t *FolderGrepTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"folder": {
				Type:        TypeString,
				Description: "Folder name. Omit to use the default mission folder.",
			},
			"path": {
				Type:        TypeString,
				Description: "Relative path to search within. Omit to search the folder root.",
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
		Required: []string{"pattern"},
	}
}

type folderGrepParams struct {
	Folder    string `json:"folder"`
	Path      string `json:"path"`
	Pattern   string `json:"pattern"`
	Recursive bool   `json:"recursive"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}

const defaultGrepLimit = 50

func (t *FolderGrepTool) Call(ctx context.Context, params string) string {
	var p folderGrepParams
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
		absPath, _, err = t.Store.ResolvePath(p.Folder, ".")
	} else {
		absPath, _, err = resolveFolderPath(t.Store, p.Folder, p.Path)
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

	grepFile := func(filePath string, relPath string) {
		f, err := os.Open(filePath)
		if err != nil {
			return
		}
		defer f.Close()

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

package wsbridge

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/mlund01/squadron-wire/protocol"

	"squadron/config"
	"squadron/mission"
)

// resolvedMemory describes one materialized memory slot for the UI: a
// human-friendly name, the absolute path it lives at, and whether agents are
// allowed to write to it.
type resolvedMemory struct {
	name     string
	path     string
	editable bool
}

// resolveMemoryPath looks up a memory slot by name (top-level shared
// memories first, then per-mission persistent memories keyed by mission name)
// and safely resolves a relative path within it.
func (c *Client) resolveMemoryPath(memoryName, relPath string) (*resolvedMemory, string, error) {
	cfg := c.getConfig()

	// Check shared memories first.
	for i := range cfg.Memories {
		if cfg.Memories[i].Name == memoryName {
			absPath, err := mission.SharedMemoryPath(memoryName)
			if err != nil {
				return nil, "", fmt.Errorf("resolve shared memory %q: %w", memoryName, err)
			}
			rm := &resolvedMemory{
				name:     cfg.Memories[i].Name,
				path:     absPath,
				editable: cfg.Memories[i].Editable,
			}
			path, err := c.resolveSafePath(absPath, relPath)
			return rm, path, err
		}
	}

	// Check the mission's persistent memory (keyed by mission name).
	for _, m := range cfg.Missions {
		if m.Memory != nil && m.Name == memoryName {
			absPath, err := mission.MissionMemoryPath(m.Name)
			if err != nil {
				return nil, "", fmt.Errorf("resolve mission memory for %q: %w", m.Name, err)
			}
			rm := &resolvedMemory{name: m.Name, path: absPath, editable: true}
			path, err := c.resolveSafePath(absPath, relPath)
			return rm, path, err
		}
	}

	return nil, "", fmt.Errorf("memory %q not found", memoryName)
}

func (c *Client) resolveSafePath(basePath, relPath string) (string, error) {
	rootPath, err := filepath.Abs(basePath)
	if err != nil {
		return "", fmt.Errorf("invalid memory path: %w", err)
	}

	if relPath == "" {
		return rootPath, nil
	}

	cleaned := filepath.Clean(relPath)
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, string(filepath.Separator)+"..") {
		return "", fmt.Errorf("invalid path")
	}

	fullPath := filepath.Join(rootPath, cleaned)
	if !strings.HasPrefix(fullPath, rootPath) {
		return "", fmt.Errorf("path escapes memory root")
	}

	return fullPath, nil
}

func (c *Client) handleListSharedFolders(env *protocol.Envelope) (*protocol.Envelope, error) {
	cfg := c.getConfig()

	folders, err := collectMemoryInfos(cfg)
	if err != nil {
		return nil, err
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeListSharedFoldersResult,
		&protocol.ListSharedFoldersResultPayload{Folders: folders})
}

// collectMemoryInfos walks the config and turns every memory slot
// (shared + per-mission persistent) into a protocol.SharedFolderInfo. Used
// by both the standalone list_shared_folders RPC and the bulk
// instance-info payload in convert.go.
func collectMemoryInfos(cfg *config.Config) ([]protocol.SharedFolderInfo, error) {
	// Build shared memory → missions map
	sharedMissions := map[string][]string{}
	for _, m := range cfg.Missions {
		for _, name := range m.Memories {
			sharedMissions[name] = append(sharedMissions[name], m.Name)
		}
	}

	var folders []protocol.SharedFolderInfo

	for _, mem := range cfg.Memories {
		label := mem.Label
		if label == "" {
			label = mem.Name
		}
		path, err := mission.SharedMemoryPath(mem.Name)
		if err != nil {
			return nil, fmt.Errorf("shared memory %q: %w", mem.Name, err)
		}
		folders = append(folders, protocol.SharedFolderInfo{
			Name:        mem.Name,
			Path:        path,
			Label:       label,
			Description: mem.Description,
			Editable:    mem.Editable,
			IsShared:    true,
			Missions:    sharedMissions[mem.Name],
		})
	}

	for _, m := range cfg.Missions {
		if m.Memory == nil {
			continue
		}
		path, err := mission.MissionMemoryPath(m.Name)
		if err != nil {
			return nil, fmt.Errorf("mission memory for %q: %w", m.Name, err)
		}
		folders = append(folders, protocol.SharedFolderInfo{
			Name:        m.Name,
			Path:        path,
			Label:       m.Name,
			Description: m.Memory.Description,
			Editable:    true,
			IsShared:    false,
			Missions:    []string{m.Name},
		})
	}

	return folders, nil
}

func (c *Client) handleBrowseDirectory(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.BrowseDirectoryPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode browse_directory: %w", err)
	}

	_, fullPath, err := c.resolveMemoryPath(payload.BrowserName, payload.RelPath)
	if err != nil {
		return nil, err
	}

	dirEntries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}

	var entries []protocol.BrowseEntryInfo
	for _, de := range dirEntries {
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		entries = append(entries, protocol.BrowseEntryInfo{
			Name:    de.Name(),
			IsDir:   de.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeBrowseDirectoryResult,
		&protocol.BrowseDirectoryResultPayload{
			BrowserName: payload.BrowserName,
			RelPath:     payload.RelPath,
			Entries:     entries,
		})
}

func (c *Client) handleReadBrowseFile(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.ReadBrowseFilePayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode read_browse_file: %w", err)
	}

	_, fullPath, err := c.resolveMemoryPath(payload.BrowserName, payload.RelPath)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory, not a file")
	}

	const maxSize = 10 * 1024 * 1024 // 10MB
	if info.Size() > maxSize {
		return protocol.NewResponse(env.RequestID, protocol.TypeReadBrowseFileResult,
			&protocol.ReadBrowseFileResultPayload{
				BrowserName: payload.BrowserName,
				RelPath:     payload.RelPath,
				Size:        info.Size(),
				IsBinary:    true,
			})
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	contentType := http.DetectContentType(content)
	isBinary := !strings.HasPrefix(contentType, "text/") &&
		contentType != "application/json" &&
		contentType != "application/xml" &&
		contentType != "application/javascript"

	textContent := ""
	if !isBinary {
		textContent = string(content)
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeReadBrowseFileResult,
		&protocol.ReadBrowseFileResultPayload{
			BrowserName: payload.BrowserName,
			RelPath:     payload.RelPath,
			Content:     textContent,
			Size:        info.Size(),
			IsBinary:    isBinary,
		})
}

func (c *Client) handleWriteBrowseFile(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.WriteBrowseFilePayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode write_browse_file: %w", err)
	}

	mem, fullPath, err := c.resolveMemoryPath(payload.BrowserName, payload.RelPath)
	if err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeWriteBrowseFileResult,
			&protocol.WriteBrowseFileResultPayload{Success: false, Error: err.Error()})
	}

	if !mem.editable {
		return protocol.NewResponse(env.RequestID, protocol.TypeWriteBrowseFileResult,
			&protocol.WriteBrowseFileResultPayload{Success: false, Error: "memory is read-only"})
	}

	if err := os.WriteFile(fullPath, []byte(payload.Content), 0644); err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeWriteBrowseFileResult,
			&protocol.WriteBrowseFileResultPayload{Success: false, Error: err.Error()})
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeWriteBrowseFileResult,
		&protocol.WriteBrowseFileResultPayload{Success: true})
}

func (c *Client) handleDownloadFile(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.DownloadFilePayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode download_file: %w", err)
	}

	_, fullPath, err := c.resolveMemoryPath(payload.BrowserName, payload.RelPath)
	if err != nil {
		return nil, err
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	contentType := http.DetectContentType(content)

	return protocol.NewResponse(env.RequestID, protocol.TypeDownloadFileResult,
		&protocol.DownloadFileResultPayload{
			Content:     base64.StdEncoding.EncodeToString(content),
			Filename:    filepath.Base(fullPath),
			ContentType: contentType,
		})
}

func (c *Client) handleDownloadDirectory(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.DownloadDirectoryPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode download_directory: %w", err)
	}

	_, fullPath, err := c.resolveMemoryPath(payload.BrowserName, payload.RelPath)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	filepath.WalkDir(fullPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, err := filepath.Rel(fullPath, path)
		if err != nil {
			return nil
		}
		fw, err := zw.Create(rel)
		if err != nil {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		fw.Write(data)
		return nil
	})
	zw.Close()

	dirName := filepath.Base(fullPath)
	return protocol.NewResponse(env.RequestID, protocol.TypeDownloadDirectoryResult,
		&protocol.DownloadDirectoryResultPayload{
			Content:  base64.StdEncoding.EncodeToString(buf.Bytes()),
			Filename: dirName + ".zip",
		})
}

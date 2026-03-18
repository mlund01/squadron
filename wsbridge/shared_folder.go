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

	"github.com/mlund01/squadron-sdk/protocol"

	"squadron/config"
)

// resolveSharedFolderPath looks up a folder by name (shared folders + mission folders)
// and safely resolves a relative path within it.
func (c *Client) resolveSharedFolderPath(folderName, relPath string) (*config.SharedFolder, string, error) {
	cfg := c.getConfig()

	// Check shared folders first
	for i := range cfg.SharedFolders {
		if cfg.SharedFolders[i].Name == folderName {
			folder := &cfg.SharedFolders[i]
			path, err := c.resolveSafePath(folder.Path, relPath)
			return folder, path, err
		}
	}

	// Check mission dedicated folders (keyed by mission name)
	for _, m := range cfg.Missions {
		if m.Folder != nil && m.Name == folderName {
			sf := &config.SharedFolder{
				Name:     m.Name,
				Path:     m.Folder.Path,
				Editable: true,
			}
			path, err := c.resolveSafePath(m.Folder.Path, relPath)
			return sf, path, err
		}
	}

	return nil, "", fmt.Errorf("folder %q not found", folderName)
}

func (c *Client) resolveSafePath(basePath, relPath string) (string, error) {
	rootPath, err := filepath.Abs(basePath)
	if err != nil {
		return "", fmt.Errorf("invalid folder path: %w", err)
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
		return "", fmt.Errorf("path escapes folder root")
	}

	return fullPath, nil
}

func (c *Client) handleListSharedFolders(env *protocol.Envelope) (*protocol.Envelope, error) {
	cfg := c.getConfig()

	// Build shared folder → missions map
	sharedMissions := map[string][]string{}
	for _, m := range cfg.Missions {
		for _, folderName := range m.Folders {
			sharedMissions[folderName] = append(sharedMissions[folderName], m.Name)
		}
	}

	var folders []protocol.SharedFolderInfo

	// Add shared folders
	for _, fb := range cfg.SharedFolders {
		label := fb.Label
		if label == "" {
			label = fb.Name
		}
		folders = append(folders, protocol.SharedFolderInfo{
			Name:        fb.Name,
			Path:        fb.Path,
			Label:       label,
			Description: fb.Description,
			Editable:    fb.Editable,
			IsShared:    true,
			Missions:    sharedMissions[fb.Name],
		})
	}

	// Add dedicated mission folders
	for _, m := range cfg.Missions {
		if m.Folder != nil {
			folders = append(folders, protocol.SharedFolderInfo{
				Name:        m.Name,
				Path:        m.Folder.Path,
				Label:       m.Name,
				Description: m.Folder.Description,
				Editable:    true,
				IsShared:    false,
				Missions:    []string{m.Name},
			})
		}
	}

	return protocol.NewResponse(env.RequestID, protocol.TypeListSharedFoldersResult,
		&protocol.ListSharedFoldersResultPayload{Folders: folders})
}

func (c *Client) handleBrowseDirectory(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.BrowseDirectoryPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode browse_directory: %w", err)
	}

	_, fullPath, err := c.resolveSharedFolderPath(payload.BrowserName, payload.RelPath)
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

	_, fullPath, err := c.resolveSharedFolderPath(payload.BrowserName, payload.RelPath)
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

	folder, fullPath, err := c.resolveSharedFolderPath(payload.BrowserName, payload.RelPath)
	if err != nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeWriteBrowseFileResult,
			&protocol.WriteBrowseFileResultPayload{Success: false, Error: err.Error()})
	}

	if !folder.Editable {
		return protocol.NewResponse(env.RequestID, protocol.TypeWriteBrowseFileResult,
			&protocol.WriteBrowseFileResultPayload{Success: false, Error: "shared folder is read-only"})
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

	_, fullPath, err := c.resolveSharedFolderPath(payload.BrowserName, payload.RelPath)
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

	_, fullPath, err := c.resolveSharedFolderPath(payload.BrowserName, payload.RelPath)
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

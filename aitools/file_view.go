package aitools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileViewTool reads an image, SVG, or PDF from any slot and returns it as a
// vision/document block the model can perceive, rather than as text. SVGs are
// rasterized to PNG. Non-viewable files are reported with a pointer to file_read.
type FileViewTool struct {
	Store MemoryStore
}

func (t *FileViewTool) ToolName() string { return "file_view" }

func (t *FileViewTool) ToolDescription() string {
	return "View an image (PNG, JPEG, GIF, WebP), an SVG, or a PDF from a slot as visual content the model can see. Use this for files file_read rejects as binary. For plain text, use file_read instead."
}

func (t *FileViewTool) ToolPayloadSchema() Schema {
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

type fileViewParams struct {
	Slot string `json:"slot"`
	Path string `json:"path"`
}

// Call satisfies Tool for callers unaware of MediaTool; it returns only the
// text summary so the media is silently dropped rather than leaking base64.
func (t *FileViewTool) Call(ctx context.Context, params string) string {
	text, _ := t.CallMedia(ctx, params)
	return text
}

func (t *FileViewTool) CallMedia(ctx context.Context, params string) (string, []MediaBlock) {
	var p fileViewParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error(), nil
	}
	if p.Path == "" {
		return "Error: path is required", nil
	}

	absPath, info, err := resolveExistingSlotFile(t.Store, p.Slot, p.Path)
	if err != nil {
		return "Error: " + err.Error(), nil
	}
	if info.Size() > maxReadSize {
		return fmt.Sprintf("Error: file too large (%s, max %s)", formatSize(info.Size()), formatSize(maxReadSize)), nil
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return "Error: " + err.Error(), nil
	}

	kind, mediaType := detectViewable(p.Path, data)
	switch kind {
	case MediaKindImage:
		return fmt.Sprintf("Loaded image %q (%s, %s).", filepath.Base(p.Path), mediaType, formatSize(info.Size())),
			[]MediaBlock{{Kind: MediaKindImage, MediaType: mediaType, Data: base64.StdEncoding.EncodeToString(data), Filename: filepath.Base(p.Path)}}
	case MediaKindDocument:
		return fmt.Sprintf("Loaded document %q (%s, %s).", filepath.Base(p.Path), mediaType, formatSize(info.Size())),
			[]MediaBlock{{Kind: MediaKindDocument, MediaType: mediaType, Data: base64.StdEncoding.EncodeToString(data), Filename: filepath.Base(p.Path)}}
	case "svg":
		png, err := rasterizeSVG(data)
		if err != nil {
			return fmt.Sprintf("Could not rasterize SVG %q (%v); use file_read to view its source markup.", filepath.Base(p.Path), err), nil
		}
		return fmt.Sprintf("Rasterized SVG %q to PNG (%s source).", filepath.Base(p.Path), formatSize(info.Size())),
			[]MediaBlock{{Kind: MediaKindImage, MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(png), Filename: filepath.Base(p.Path) + ".png"}}
	default:
		return fmt.Sprintf("File %q is not a viewable image, SVG, or PDF; use file_read for text content.", filepath.Base(p.Path)), nil
	}
}

// detectViewable classifies a file as image/document/svg by magic bytes first,
// then by a .svg extension or an <svg sniff. Returns "" when not viewable.
func detectViewable(name string, data []byte) (kind, mediaType string) {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return MediaKindImage, "image/png"
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return MediaKindImage, "image/jpeg"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return MediaKindImage, "image/gif"
	case len(data) >= 12 && bytes.Equal(data[0:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return MediaKindImage, "image/webp"
	case bytes.HasPrefix(data, []byte("%PDF")):
		return MediaKindDocument, "application/pdf"
	}
	if strings.EqualFold(filepath.Ext(name), ".svg") || looksLikeSVG(data) {
		return "svg", "image/svg+xml"
	}
	return "", ""
}

// looksLikeSVG sniffs the leading bytes for an <svg root, tolerating an XML
// declaration or leading whitespace.
func looksLikeSVG(data []byte) bool {
	head := data
	if len(head) > 1024 {
		head = head[:1024]
	}
	return bytes.Contains(bytes.ToLower(head), []byte("<svg"))
}

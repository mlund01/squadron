package aitools

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// NestedImgExampleTool returns a JSON object with text and embedded images for testing multimodal observations.
type NestedImgExampleTool struct{}

func (t *NestedImgExampleTool) ToolName() string {
	return "nested_img_example"
}

func (t *NestedImgExampleTool) ToolDescription() string {
	return "Test tool that reads one or more image files and returns a JSON object containing a question and the embedded images. Used to verify multimodal observation handling."
}

func (t *NestedImgExampleTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"paths": {
				Type:        TypeArray,
				Description: "List of image file paths to read.",
				Items:       &Property{Type: TypeString},
			},
			"question": {
				Type:        TypeString,
				Description: "Question to ask about the images. Default: 'What is in these images?'",
			},
		},
		Required: []string{"paths"},
	}
}

type nestedImgParams struct {
	Paths    []string `json:"paths"`
	Question string   `json:"question"`
}

func (t *NestedImgExampleTool) Call(params string) string {
	var p nestedImgParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if len(p.Paths) == 0 {
		return "Error: at least one path is required"
	}
	if p.Question == "" {
		p.Question = "What is in these images?"
	}

	images := make([]string, 0, len(p.Paths))
	for _, path := range p.Paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Sprintf("Error reading file %q: %v", path, err)
		}
		mediaType := detectMIME(path, data)
		b64 := base64.StdEncoding.EncodeToString(data)
		images = append(images, fmt.Sprintf("data:%s;base64,%s", mediaType, b64))
	}

	result := map[string]any{
		"question": p.Question,
		"images":   images,
	}

	out, _ := json.Marshal(result)
	return string(out)
}

func detectMIME(path string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return http.DetectContentType(data)
	}
}

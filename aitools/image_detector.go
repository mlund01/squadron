package aitools

import (
	"regexp"
	"strings"
)

// DetectedImage represents a detected base64-encoded image
type DetectedImage struct {
	Data      string // Base64 data (without data URL prefix)
	MediaType string // MIME type: "image/png", "image/jpeg", etc.
}

// dataURLPattern matches data URLs for images
var dataURLPattern = regexp.MustCompile(`^data:image/(png|jpeg|jpg|gif|webp);base64,(.+)$`)

// DetectImage checks if a string contains base64-encoded image data
// Returns nil if no image is detected
func DetectImage(result string) *DetectedImage {
	result = strings.TrimSpace(result)

	// Check for data URL format first (most common from tools like Playwright)
	if matches := dataURLPattern.FindStringSubmatch(result); matches != nil {
		mediaType := "image/" + matches[1]
		if matches[1] == "jpg" {
			mediaType = "image/jpeg"
		}
		return &DetectedImage{
			Data:      matches[2],
			MediaType: mediaType,
		}
	}

	// Check for raw base64 signatures
	// These are the base64-encoded versions of common image file signatures

	// PNG: starts with 0x89 0x50 0x4E 0x47 (base64: iVBORw0KGgo)
	if strings.HasPrefix(result, "iVBORw0KGgo") {
		return &DetectedImage{
			Data:      result,
			MediaType: "image/png",
		}
	}

	// JPEG: starts with 0xFF 0xD8 0xFF (base64: /9j/)
	if strings.HasPrefix(result, "/9j/") {
		return &DetectedImage{
			Data:      result,
			MediaType: "image/jpeg",
		}
	}

	// GIF: starts with "GIF89a" or "GIF87a" (base64: R0lGOD)
	if strings.HasPrefix(result, "R0lGOD") {
		return &DetectedImage{
			Data:      result,
			MediaType: "image/gif",
		}
	}

	// WebP: starts with "RIFF" (base64: UklGR)
	// Note: WebP files start with RIFF....WEBP, so we check for RIFF prefix
	if strings.HasPrefix(result, "UklGR") {
		return &DetectedImage{
			Data:      result,
			MediaType: "image/webp",
		}
	}

	return nil
}

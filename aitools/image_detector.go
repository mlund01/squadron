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

// ImageExtractionResult holds extracted images and any remaining text
type ImageExtractionResult struct {
	Images       []DetectedImage
	RemainingText string // Original text with image data replaced by placeholders
}

// dataURLPattern matches data URLs for images anywhere in a string
var dataURLPattern = regexp.MustCompile(`data:image/(png|jpeg|jpg|gif|webp);base64,([A-Za-z0-9+/=]+)`)

// jsonBase64Pattern matches base64 image data inside JSON string values
// Looks for "key": "iVBOR..." or "key": "/9j/..." etc.
var jsonBase64Pattern = regexp.MustCompile(`"[^"]*":\s*"((?:iVBORw0KGgo|/9j/|R0lGOD|UklGR)[A-Za-z0-9+/=]{100,})"`)

// rawBase64Signatures maps base64 magic byte prefixes to MIME types
var rawBase64Signatures = []struct {
	prefix    string
	mediaType string
}{
	{"iVBORw0KGgo", "image/png"},
	{"/9j/", "image/jpeg"},
	{"R0lGOD", "image/gif"},
	{"UklGR", "image/webp"},
}

// DetectImage checks if a string contains a single base64-encoded image.
// Returns nil if no image is detected. For multi-image extraction, use ExtractImages.
func DetectImage(result string) *DetectedImage {
	r := ExtractImages(result)
	if len(r.Images) == 0 {
		return nil
	}
	return &r.Images[0]
}

// ExtractImages finds all base64-encoded images in a string.
// Handles: data URLs, raw base64 blobs, and base64 image data inside JSON string values.
// Returns extracted images and the remaining text with image data replaced by [image] placeholders.
func ExtractImages(result string) ImageExtractionResult {
	result = strings.TrimSpace(result)
	if result == "" {
		return ImageExtractionResult{}
	}

	var images []DetectedImage
	remaining := result

	// 1. Extract data URL images
	remaining = dataURLPattern.ReplaceAllStringFunc(remaining, func(match string) string {
		matches := dataURLPattern.FindStringSubmatch(match)
		if matches == nil {
			return match
		}
		mediaType := "image/" + matches[1]
		if matches[1] == "jpg" {
			mediaType = "image/jpeg"
		}
		images = append(images, DetectedImage{
			Data:      matches[2],
			MediaType: mediaType,
		})
		return "[image]"
	})

	// 2. Extract base64 images from JSON string values
	remaining = jsonBase64Pattern.ReplaceAllStringFunc(remaining, func(match string) string {
		matches := jsonBase64Pattern.FindStringSubmatch(match)
		if matches == nil {
			return match
		}
		b64Data := matches[1]
		mediaType := detectMediaType(b64Data)
		if mediaType == "" {
			return match
		}
		images = append(images, DetectedImage{
			Data:      b64Data,
			MediaType: mediaType,
		})
		// Replace just the base64 value, keep the key
		return strings.Replace(match, b64Data, "[image]", 1)
	})

	// 3. If no images found yet and the entire string looks like raw base64, check magic bytes
	if len(images) == 0 {
		mediaType := detectMediaType(remaining)
		if mediaType != "" && isLikelyBase64(remaining) {
			images = append(images, DetectedImage{
				Data:      remaining,
				MediaType: mediaType,
			})
			remaining = "[image]"
		}
	}

	remaining = strings.TrimSpace(remaining)

	return ImageExtractionResult{
		Images:        images,
		RemainingText: remaining,
	}
}

// detectMediaType checks if a string starts with a known base64 image signature
func detectMediaType(s string) string {
	for _, sig := range rawBase64Signatures {
		if strings.HasPrefix(s, sig.prefix) {
			return sig.mediaType
		}
	}
	return ""
}

// isLikelyBase64 checks if a string looks like it's entirely base64 data (no spaces, newlines, etc.)
func isLikelyBase64(s string) bool {
	if len(s) < 20 {
		return false
	}
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=' || c == '\n' || c == '\r') {
			return false
		}
	}
	return true
}

package aitools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const userAgent = "squad-cli"

var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

// HTTPGetTool performs HTTP GET requests
type HTTPGetTool struct{}

func (t *HTTPGetTool) ToolName() string {
	return "http_get"
}

func (t *HTTPGetTool) ToolDescription() string {
	return "Performs an HTTP GET request to the specified URL and returns the response body."
}

func (t *HTTPGetTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"url": {
				Type:        TypeString,
				Description: "The URL to send the GET request to",
			},
			"headers": {
				Type:        TypeObject,
				Description: "Optional headers to include in the request (key-value pairs)",
			},
		},
		Required: []string{"url"},
	}
}

type httpGetParams struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func (t *HTTPGetTool) Call(params string) string {
	var p httpGetParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.URL == "" {
		return "Error: url is required"
	}

	req, err := http.NewRequest("GET", p.URL, nil)
	if err != nil {
		return "Error: failed to create request - " + err.Error()
	}

	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}

	return executeRequest(req)
}

// HTTPPostTool performs HTTP POST requests
type HTTPPostTool struct{}

func (t *HTTPPostTool) ToolName() string {
	return "http_post"
}

func (t *HTTPPostTool) ToolDescription() string {
	return "Performs an HTTP POST request to the specified URL with a body and returns the response. Supports JSON, form data, and plain text content types."
}

func (t *HTTPPostTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"url": {
				Type:        TypeString,
				Description: "The URL to send the POST request to",
			},
			"body": {
				Type:        TypeObject,
				Description: "The body to send (object for JSON/form, string for text)",
			},
			"content_type": {
				Type:        TypeString,
				Description: "Content type: 'json' (default), 'form', or 'text'",
			},
			"headers": {
				Type:        TypeObject,
				Description: "Optional headers to include in the request (key-value pairs)",
			},
		},
		Required: []string{"url"},
	}
}

type httpBodyParams struct {
	URL         string            `json:"url"`
	Body        any               `json:"body"`
	ContentType string            `json:"content_type"`
	Headers     map[string]string `json:"headers"`
}

func (t *HTTPPostTool) Call(params string) string {
	var p httpBodyParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.URL == "" {
		return "Error: url is required"
	}

	return executeBodyRequest("POST", p)
}

// HTTPPutTool performs HTTP PUT requests
type HTTPPutTool struct{}

func (t *HTTPPutTool) ToolName() string {
	return "http_put"
}

func (t *HTTPPutTool) ToolDescription() string {
	return "Performs an HTTP PUT request to the specified URL with a body and returns the response. Supports JSON, form data, and plain text content types."
}

func (t *HTTPPutTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"url": {
				Type:        TypeString,
				Description: "The URL to send the PUT request to",
			},
			"body": {
				Type:        TypeObject,
				Description: "The body to send (object for JSON/form, string for text)",
			},
			"content_type": {
				Type:        TypeString,
				Description: "Content type: 'json' (default), 'form', or 'text'",
			},
			"headers": {
				Type:        TypeObject,
				Description: "Optional headers to include in the request (key-value pairs)",
			},
		},
		Required: []string{"url"},
	}
}

func (t *HTTPPutTool) Call(params string) string {
	var p httpBodyParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.URL == "" {
		return "Error: url is required"
	}

	return executeBodyRequest("PUT", p)
}

// HTTPPatchTool performs HTTP PATCH requests
type HTTPPatchTool struct{}

func (t *HTTPPatchTool) ToolName() string {
	return "http_patch"
}

func (t *HTTPPatchTool) ToolDescription() string {
	return "Performs an HTTP PATCH request to the specified URL with a body and returns the response. Supports JSON, form data, and plain text content types."
}

func (t *HTTPPatchTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"url": {
				Type:        TypeString,
				Description: "The URL to send the PATCH request to",
			},
			"body": {
				Type:        TypeObject,
				Description: "The body to send (object for JSON/form, string for text)",
			},
			"content_type": {
				Type:        TypeString,
				Description: "Content type: 'json' (default), 'form', or 'text'",
			},
			"headers": {
				Type:        TypeObject,
				Description: "Optional headers to include in the request (key-value pairs)",
			},
		},
		Required: []string{"url"},
	}
}

func (t *HTTPPatchTool) Call(params string) string {
	var p httpBodyParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.URL == "" {
		return "Error: url is required"
	}

	return executeBodyRequest("PATCH", p)
}

// HTTPDeleteTool performs HTTP DELETE requests
type HTTPDeleteTool struct{}

func (t *HTTPDeleteTool) ToolName() string {
	return "http_delete"
}

func (t *HTTPDeleteTool) ToolDescription() string {
	return "Performs an HTTP DELETE request to the specified URL and returns the response."
}

func (t *HTTPDeleteTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"url": {
				Type:        TypeString,
				Description: "The URL to send the DELETE request to",
			},
			"headers": {
				Type:        TypeObject,
				Description: "Optional headers to include in the request (key-value pairs)",
			},
		},
		Required: []string{"url"},
	}
}

func (t *HTTPDeleteTool) Call(params string) string {
	var p httpGetParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.URL == "" {
		return "Error: url is required"
	}

	req, err := http.NewRequest("DELETE", p.URL, nil)
	if err != nil {
		return "Error: failed to create request - " + err.Error()
	}

	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}

	return executeRequest(req)
}

// Helper functions

func executeBodyRequest(method string, p httpBodyParams) string {
	var bodyReader io.Reader
	var contentType string

	if p.Body != nil {
		// Default to JSON if not specified
		ct := p.ContentType
		if ct == "" {
			ct = "json"
		}

		switch ct {
		case "json":
			bodyBytes, err := json.Marshal(p.Body)
			if err != nil {
				return "Error: failed to marshal JSON body - " + err.Error()
			}
			bodyReader = bytes.NewReader(bodyBytes)
			contentType = "application/json"

		case "form":
			// Body should be a map for form data
			bodyMap, ok := p.Body.(map[string]any)
			if !ok {
				return "Error: form content_type requires body to be an object with string values"
			}
			values := make(url.Values)
			for k, v := range bodyMap {
				values.Set(k, fmt.Sprintf("%v", v))
			}
			bodyReader = strings.NewReader(values.Encode())
			contentType = "application/x-www-form-urlencoded"

		case "text":
			// Body should be a string for plain text
			text, ok := p.Body.(string)
			if !ok {
				return "Error: text content_type requires body to be a string"
			}
			bodyReader = strings.NewReader(text)
			contentType = "text/plain"

		default:
			return "Error: unsupported content_type '" + ct + "' - use 'json', 'form', or 'text'"
		}
	}

	req, err := http.NewRequest(method, p.URL, bodyReader)
	if err != nil {
		return "Error: failed to create request - " + err.Error()
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}

	return executeRequest(req)
}

func executeRequest(req *http.Request) string {
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "Error: request failed - " + err.Error()
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "Error: failed to read response - " + err.Error()
	}

	return fmt.Sprintf("Status: %d %s\n\n%s", resp.StatusCode, resp.Status, string(body))
}

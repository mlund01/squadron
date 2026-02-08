package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/playwright-community/playwright-go"

	squadron "github.com/mlund01/squad-sdk"
)

// whitespaceRegex matches multiple consecutive whitespace characters
var whitespaceRegex = regexp.MustCompile(`[ \t]+`)

// emptyLineRegex matches lines that are empty or contain only whitespace
var emptyLineRegex = regexp.MustCompile(`(?m)^\s*$[\r\n]*`)

// sessionIDProperty is added to all tools that need a session
var sessionIDProperty = squadron.Property{
	Type:        squadron.TypeString,
	Description: "Session ID from browser_new_session. Required - sessions must be explicitly created first.",
}

// tools holds the metadata for each tool provided by this plugin
var tools = map[string]*squadron.ToolInfo{
	"browser_new_session": {
		Name:        "browser_new_session",
		Description: "Create a new browser session. Returns a session_id to use with other browser tools. Provider (local/azure) is configured at the plugin level.",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"headless": {
					Type:        squadron.TypeBoolean,
					Description: "Run browser in headless mode. Defaults to plugin setting. Only applies to local provider.",
				},
				"browser_type": {
					Type:        squadron.TypeString,
					Description: "Browser to use: 'chromium', 'firefox', or 'webkit'. Defaults to plugin setting. Only applies to local provider.",
				},
			},
		},
	},
	"browser_list_sessions": {
		Name:        "browser_list_sessions",
		Description: "List all active browser sessions",
		Schema: squadron.Schema{
			Type:       squadron.TypeObject,
			Properties: squadron.PropertyMap{},
		},
	},
	"browser_navigate": {
		Name:        "browser_navigate",
		Description: "Navigate the browser to a URL",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"session_id": sessionIDProperty,
				"url": {
					Type:        squadron.TypeString,
					Description: "The URL to navigate to",
				},
				"wait_until": {
					Type:        squadron.TypeString,
					Description: "When to consider navigation complete: 'load', 'domcontentloaded', 'networkidle'. Defaults to 'load'",
				},
			},
			Required: []string{"session_id", "url"},
		},
	},
	"browser_click": {
		Name:        "browser_click",
		Description: "Click on an element in the page using a CSS selector",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"session_id": sessionIDProperty,
				"selector": {
					Type:        squadron.TypeString,
					Description: "CSS selector for the element to click",
				},
			},
			Required: []string{"session_id", "selector"},
		},
	},
	"browser_type": {
		Name:        "browser_type",
		Description: "Type text into an input element",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"session_id": sessionIDProperty,
				"selector": {
					Type:        squadron.TypeString,
					Description: "CSS selector for the input element",
				},
				"text": {
					Type:        squadron.TypeString,
					Description: "Text to type into the element",
				},
				"clear": {
					Type:        squadron.TypeBoolean,
					Description: "Clear the input before typing. Defaults to false",
				},
			},
			Required: []string{"session_id", "selector", "text"},
		},
	},
	"browser_screenshot": {
		Name:        "browser_screenshot",
		Description: "Take a screenshot of the current page. Returns a JPEG image by default (smaller size for faster processing).",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"session_id": sessionIDProperty,
				"path": {
					Type:        squadron.TypeString,
					Description: "File path to save the screenshot. If not provided, returns base64-encoded image",
				},
				"full_page": {
					Type:        squadron.TypeBoolean,
					Description: "Capture the full scrollable page. Defaults to false",
				},
				"selector": {
					Type:        squadron.TypeString,
					Description: "CSS selector to screenshot a specific element instead of the page",
				},
				"quality": {
					Type:        squadron.TypeInteger,
					Description: "JPEG quality (1-100). Defaults to 80. Lower values produce smaller images.",
				},
			},
			Required: []string{"session_id"},
		},
	},
	"browser_get_text": {
		Name:        "browser_get_text",
		Description: "Get the text content of an element or the entire page",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"session_id": sessionIDProperty,
				"selector": {
					Type:        squadron.TypeString,
					Description: "CSS selector for the element. If not provided, gets text from body",
				},
			},
			Required: []string{"session_id"},
		},
	},
	"browser_get_html": {
		Name:        "browser_get_html",
		Description: "Get the HTML content of an element or the entire page",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"session_id": sessionIDProperty,
				"selector": {
					Type:        squadron.TypeString,
					Description: "CSS selector for the element. If not provided, gets HTML from body",
				},
				"outer": {
					Type:        squadron.TypeBoolean,
					Description: "Get outer HTML (including the element itself). Defaults to false (inner HTML)",
				},
			},
			Required: []string{"session_id"},
		},
	},
	"browser_evaluate": {
		Name:        "browser_evaluate",
		Description: "Execute JavaScript code in the browser and return the result",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"session_id": sessionIDProperty,
				"script": {
					Type:        squadron.TypeString,
					Description: "JavaScript code to execute. Should return a serializable value",
				},
			},
			Required: []string{"session_id", "script"},
		},
	},
	"browser_wait_for_selector": {
		Name:        "browser_wait_for_selector",
		Description: "Wait for an element matching the selector to appear in the page",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"session_id": sessionIDProperty,
				"selector": {
					Type:        squadron.TypeString,
					Description: "CSS selector to wait for",
				},
				"state": {
					Type:        squadron.TypeString,
					Description: "State to wait for: 'attached', 'detached', 'visible', 'hidden'. Defaults to 'visible'",
				},
				"timeout": {
					Type:        squadron.TypeInteger,
					Description: "Maximum time to wait in milliseconds. Defaults to 30000",
				},
			},
			Required: []string{"session_id", "selector"},
		},
	},
	"browser_close_session": {
		Name:        "browser_close_session",
		Description: "Close a specific browser session",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"session_id": {
					Type:        squadron.TypeString,
					Description: "Session ID to close",
				},
			},
			Required: []string{"session_id"},
		},
	},
	"browser_close_all": {
		Name:        "browser_close_all",
		Description: "Close all browser sessions and cleanup",
		Schema: squadron.Schema{
			Type:       squadron.TypeObject,
			Properties: squadron.PropertyMap{},
		},
	},
	"browser_aria_snapshot": {
		Name:        "browser_aria_snapshot",
		Description: "Get an ARIA accessibility tree snapshot of the page. Returns a YAML representation of the accessibility tree, which is more compact and AI-friendly than screenshots for understanding page structure and content.",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"session_id": sessionIDProperty,
				"selector": {
					Type:        squadron.TypeString,
					Description: "CSS selector to get snapshot of a specific element. If not provided, gets snapshot of the entire page (body)",
				},
			},
			Required: []string{"session_id"},
		},
	},
	"browser_click_coordinates": {
		Name:        "browser_click_coordinates",
		Description: "Click at specific x,y coordinates on the page. Useful when CSS selectors fail or for clicking on canvas/complex UI elements.",
		Schema: squadron.Schema{
			Type: squadron.TypeObject,
			Properties: squadron.PropertyMap{
				"session_id": sessionIDProperty,
				"x": {
					Type:        squadron.TypeNumber,
					Description: "X coordinate (horizontal position from left edge)",
				},
				"y": {
					Type:        squadron.TypeNumber,
					Description: "Y coordinate (vertical position from top edge)",
				},
			},
			Required: []string{"session_id", "x", "y"},
		},
	},
}

// browserSession represents a single browser session
type browserSession struct {
	browser     playwright.Browser
	page        playwright.Page
	isAzure     bool
	browserType string
}

// pluginSettings holds the configured settings for this plugin
type pluginSettings struct {
	provider      string // "local" or "azure"
	azureEndpoint string
	headless      bool
	browserType   string // "chromium", "firefox", "webkit"
}

// PlaywrightPlugin implements the ToolProvider interface
type PlaywrightPlugin struct {
	mu       sync.Mutex
	pw       *playwright.Playwright
	sessions map[string]*browserSession
	seq      int
	settings pluginSettings
}

// Configure applies settings from HCL config
func (p *PlaywrightPlugin) Configure(settings map[string]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Set defaults
	p.settings = pluginSettings{
		provider:    "local",
		headless:    true,
		browserType: "chromium",
	}

	// Apply settings
	if v, ok := settings["provider"]; ok {
		if v != "local" && v != "azure" {
			return fmt.Errorf("invalid provider '%s': must be 'local' or 'azure'", v)
		}
		p.settings.provider = v
	}

	if v, ok := settings["azure_endpoint"]; ok {
		p.settings.azureEndpoint = v
	}

	if v, ok := settings["headless"]; ok {
		p.settings.headless = v == "true"
	}

	if v, ok := settings["browser_type"]; ok {
		if v != "chromium" && v != "firefox" && v != "webkit" {
			return fmt.Errorf("invalid browser_type '%s': must be 'chromium', 'firefox', or 'webkit'", v)
		}
		p.settings.browserType = v
	}

	// Validate azure settings
	if p.settings.provider == "azure" && p.settings.azureEndpoint == "" {
		return fmt.Errorf("azure_endpoint is required when provider is 'azure'")
	}

	return nil
}

// ensurePlaywright makes sure the playwright instance is running
func (p *PlaywrightPlugin) ensurePlaywright() error {
	if p.pw != nil {
		return nil
	}

	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("could not start playwright: %w", err)
	}
	p.pw = pw
	return nil
}

// getSession returns the session with the given ID.
// Sessions must be explicitly created with browser_new_session first.
func (p *PlaywrightPlugin) getSession(sessionID string) (*browserSession, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required - use browser_new_session to create a session first")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.sessions == nil {
		p.sessions = make(map[string]*browserSession)
	}

	session, exists := p.sessions[sessionID]
	if !exists {
		return nil, fmt.Errorf("session '%s' not found - use browser_new_session to create it first", sessionID)
	}

	return session, nil
}

func (p *PlaywrightPlugin) Call(toolName string, payload string) (string, error) {
	switch toolName {
	case "browser_new_session":
		return p.newSession(payload)
	case "browser_list_sessions":
		return p.listSessions(payload)
	case "browser_navigate":
		return p.navigate(payload)
	case "browser_click":
		return p.click(payload)
	case "browser_type":
		return p.typeText(payload)
	case "browser_screenshot":
		return p.screenshot(payload)
	case "browser_get_text":
		return p.getText(payload)
	case "browser_get_html":
		return p.getHTML(payload)
	case "browser_evaluate":
		return p.evaluate(payload)
	case "browser_wait_for_selector":
		return p.waitForSelector(payload)
	case "browser_close_session":
		return p.closeSession(payload)
	case "browser_close_all":
		return p.closeAll(payload)
	case "browser_aria_snapshot":
		return p.ariaSnapshot(payload)
	case "browser_click_coordinates":
		return p.clickCoordinates(payload)
	default:
		return "", fmt.Errorf("unknown tool: %s", toolName)
	}
}

func (p *PlaywrightPlugin) newSession(payload string) (string, error) {
	var params struct {
		Headless    *bool  `json:"headless"`
		BrowserType string `json:"browser_type"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		return "", fmt.Errorf("invalid payload: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.sessions == nil {
		p.sessions = make(map[string]*browserSession)
	}

	if err := p.ensurePlaywright(); err != nil {
		return "", err
	}

	// Generate session ID
	p.seq++
	sessionID := fmt.Sprintf("session_%d", p.seq)

	// Use plugin settings, with optional per-session overrides
	headless := p.settings.headless
	if params.Headless != nil {
		headless = *params.Headless
	}

	browserType := p.settings.browserType
	if params.BrowserType != "" {
		browserType = params.BrowserType
	}

	var browser playwright.Browser
	var err error
	isAzure := false

	if p.settings.provider == "azure" {
		// Connect to Azure Playwright using plugin settings
		browser, err = p.pw.Chromium.Connect(p.settings.azureEndpoint)
		if err != nil {
			return "", fmt.Errorf("could not connect to Azure Playwright: %w", err)
		}
		isAzure = true
		browserType = "chromium" // Azure primarily uses Chromium
	} else {
		// Local browser
		launchOpts := playwright.BrowserTypeLaunchOptions{
			Headless: playwright.Bool(headless),
		}

		switch browserType {
		case "firefox":
			browser, err = p.pw.Firefox.Launch(launchOpts)
		case "webkit":
			browser, err = p.pw.WebKit.Launch(launchOpts)
		default:
			browserType = "chromium"
			browser, err = p.pw.Chromium.Launch(launchOpts)
		}

		if err != nil {
			return "", fmt.Errorf("could not launch browser: %w", err)
		}
	}

	page, err := browser.NewPage()
	if err != nil {
		browser.Close()
		return "", fmt.Errorf("could not create page: %w", err)
	}

	p.sessions[sessionID] = &browserSession{
		browser:     browser,
		page:        page,
		isAzure:     isAzure,
		browserType: browserType,
	}

	providerLabel := "local"
	if isAzure {
		providerLabel = "azure"
	}

	return fmt.Sprintf("Created session '%s' (provider: %s, browser: %s)", sessionID, providerLabel, browserType), nil
}

func (p *PlaywrightPlugin) listSessions(payload string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.sessions) == 0 {
		return "No active sessions", nil
	}

	var result string
	for id, session := range p.sessions {
		provider := "local"
		if session.isAzure {
			provider = "azure"
		}
		url := ""
		if session.page != nil {
			url = session.page.URL()
		}
		result += fmt.Sprintf("- %s (provider: %s, browser: %s, url: %s)\n", id, provider, session.browserType, url)
	}

	return result, nil
}

func (p *PlaywrightPlugin) navigate(payload string) (string, error) {
	var params struct {
		SessionID string `json:"session_id"`
		URL       string `json:"url"`
		WaitUntil string `json:"wait_until"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		return "", fmt.Errorf("invalid payload: %w", err)
	}

	session, err := p.getSession(params.SessionID)
	if err != nil {
		return "", err
	}

	waitUntil := playwright.WaitUntilStateLoad
	if params.WaitUntil != "" {
		switch params.WaitUntil {
		case "domcontentloaded":
			waitUntil = playwright.WaitUntilStateDomcontentloaded
		case "networkidle":
			waitUntil = playwright.WaitUntilStateNetworkidle
		}
	}

	_, err = session.page.Goto(params.URL, playwright.PageGotoOptions{
		WaitUntil: waitUntil,
	})
	if err != nil {
		return "", fmt.Errorf("navigation failed: %w", err)
	}

	title, _ := session.page.Title()
	return fmt.Sprintf("[%s] Navigated to %s (title: %s)", params.SessionID, params.URL, title), nil
}

func (p *PlaywrightPlugin) click(payload string) (string, error) {
	var params struct {
		SessionID string `json:"session_id"`
		Selector  string `json:"selector"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		return "", fmt.Errorf("invalid payload: %w", err)
	}

	session, err := p.getSession(params.SessionID)
	if err != nil {
		return "", err
	}

	if err := session.page.Click(params.Selector); err != nil {
		return "", fmt.Errorf("click failed: %w", err)
	}

	return fmt.Sprintf("Clicked element: %s", params.Selector), nil
}

func (p *PlaywrightPlugin) typeText(payload string) (string, error) {
	var params struct {
		SessionID string `json:"session_id"`
		Selector  string `json:"selector"`
		Text      string `json:"text"`
		Clear     bool   `json:"clear"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		return "", fmt.Errorf("invalid payload: %w", err)
	}

	session, err := p.getSession(params.SessionID)
	if err != nil {
		return "", err
	}

	if params.Clear {
		if err := session.page.Fill(params.Selector, ""); err != nil {
			return "", fmt.Errorf("clear failed: %w", err)
		}
	}

	if err := session.page.Fill(params.Selector, params.Text); err != nil {
		return "", fmt.Errorf("type failed: %w", err)
	}

	return fmt.Sprintf("Typed text into: %s", params.Selector), nil
}

func (p *PlaywrightPlugin) screenshot(payload string) (string, error) {
	var params struct {
		SessionID string `json:"session_id"`
		Path      string `json:"path"`
		FullPage  bool   `json:"full_page"`
		Selector  string `json:"selector"`
		Quality   int    `json:"quality"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		return "", fmt.Errorf("invalid payload: %w", err)
	}

	session, err := p.getSession(params.SessionID)
	if err != nil {
		return "", err
	}

	// Default JPEG quality
	quality := params.Quality
	if quality <= 0 || quality > 100 {
		quality = 80
	}

	// Build screenshot options: JPEG format, CSS scale (1x regardless of device DPI)
	opts := playwright.PageScreenshotOptions{
		FullPage: playwright.Bool(params.FullPage),
		Type:     playwright.ScreenshotTypeJpeg,
		Quality:  playwright.Int(quality),
		Scale:    playwright.ScreenshotScaleCss,
	}

	var imageBytes []byte

	if params.Selector != "" {
		element, err := session.page.QuerySelector(params.Selector)
		if err != nil {
			return "", fmt.Errorf("selector failed: %w", err)
		}
		if element == nil {
			return "", fmt.Errorf("element not found: %s", params.Selector)
		}
		imageBytes, err = element.Screenshot(playwright.ElementHandleScreenshotOptions{
			Type:    playwright.ScreenshotTypeJpeg,
			Quality: playwright.Int(quality),
			Scale:   playwright.ScreenshotScaleCss,
		})
		if err != nil {
			return "", fmt.Errorf("screenshot failed: %w", err)
		}
	} else {
		imageBytes, err = session.page.Screenshot(opts)
		if err != nil {
			return "", fmt.Errorf("screenshot failed: %w", err)
		}
	}

	if params.Path != "" {
		saveOpts := opts
		saveOpts.Path = playwright.String(params.Path)
		_, err := session.page.Screenshot(saveOpts)
		if err != nil {
			return "", fmt.Errorf("save screenshot failed: %w", err)
		}
		return fmt.Sprintf("Screenshot saved to: %s", params.Path), nil
	}

	encoded := base64.StdEncoding.EncodeToString(imageBytes)
	return fmt.Sprintf("data:image/jpeg;base64,%s", encoded), nil
}

func (p *PlaywrightPlugin) getText(payload string) (string, error) {
	var params struct {
		SessionID string `json:"session_id"`
		Selector  string `json:"selector"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		return "", fmt.Errorf("invalid payload: %w", err)
	}

	session, err := p.getSession(params.SessionID)
	if err != nil {
		return "", err
	}

	selector := params.Selector
	if selector == "" {
		selector = "body"
	}

	text, err := session.page.TextContent(selector)
	if err != nil {
		return "", fmt.Errorf("get text failed: %w", err)
	}

	// Normalize whitespace: collapse multiple spaces/tabs into single space
	text = whitespaceRegex.ReplaceAllString(text, " ")
	// Remove empty lines
	text = emptyLineRegex.ReplaceAllString(text, "\n")
	// Trim each line and remove leading/trailing newlines
	lines := strings.Split(text, "\n")
	var cleanLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			cleanLines = append(cleanLines, trimmed)
		}
	}
	return strings.Join(cleanLines, "\n"), nil
}

func (p *PlaywrightPlugin) getHTML(payload string) (string, error) {
	var params struct {
		SessionID string `json:"session_id"`
		Selector  string `json:"selector"`
		Outer     bool   `json:"outer"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		return "", fmt.Errorf("invalid payload: %w", err)
	}

	session, err := p.getSession(params.SessionID)
	if err != nil {
		return "", err
	}

	selector := params.Selector
	if selector == "" {
		selector = "body"
	}

	if params.Outer {
		element, err := session.page.QuerySelector(selector)
		if err != nil {
			return "", fmt.Errorf("selector failed: %w", err)
		}
		if element == nil {
			return "", fmt.Errorf("element not found: %s", selector)
		}
		html, err := element.Evaluate("el => el.outerHTML", nil)
		if err != nil {
			return "", fmt.Errorf("get outer HTML failed: %w", err)
		}
		return fmt.Sprintf("%v", html), nil
	}

	html, err := session.page.InnerHTML(selector)
	if err != nil {
		return "", fmt.Errorf("get HTML failed: %w", err)
	}

	return html, nil
}

func (p *PlaywrightPlugin) evaluate(payload string) (string, error) {
	var params struct {
		SessionID string `json:"session_id"`
		Script    string `json:"script"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		return "", fmt.Errorf("invalid payload: %w", err)
	}

	session, err := p.getSession(params.SessionID)
	if err != nil {
		return "", err
	}

	result, err := session.page.Evaluate(params.Script)
	if err != nil {
		return "", fmt.Errorf("evaluate failed: %w", err)
	}

	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf("%v", result), nil
	}

	return string(jsonBytes), nil
}

func (p *PlaywrightPlugin) waitForSelector(payload string) (string, error) {
	var params struct {
		SessionID string `json:"session_id"`
		Selector  string `json:"selector"`
		State     string `json:"state"`
		Timeout   int    `json:"timeout"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		return "", fmt.Errorf("invalid payload: %w", err)
	}

	session, err := p.getSession(params.SessionID)
	if err != nil {
		return "", err
	}

	state := playwright.WaitForSelectorStateVisible
	if params.State != "" {
		switch params.State {
		case "attached":
			state = playwright.WaitForSelectorStateAttached
		case "detached":
			state = playwright.WaitForSelectorStateDetached
		case "hidden":
			state = playwright.WaitForSelectorStateHidden
		}
	}

	timeout := float64(30000)
	if params.Timeout > 0 {
		timeout = float64(params.Timeout)
	}

	_, err = session.page.WaitForSelector(params.Selector, playwright.PageWaitForSelectorOptions{
		State:   state,
		Timeout: playwright.Float(timeout),
	})
	if err != nil {
		return "", fmt.Errorf("wait failed: %w", err)
	}

	return fmt.Sprintf("Element found: %s", params.Selector), nil
}

func (p *PlaywrightPlugin) closeSession(payload string) (string, error) {
	var params struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		return "", fmt.Errorf("invalid payload: %w", err)
	}

	if params.SessionID == "" {
		return "", fmt.Errorf("session_id is required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	session, exists := p.sessions[params.SessionID]
	if !exists {
		return fmt.Sprintf("Session '%s' not found", params.SessionID), nil
	}

	if session.page != nil {
		session.page.Close()
	}
	if session.browser != nil {
		session.browser.Close()
	}

	delete(p.sessions, params.SessionID)

	return fmt.Sprintf("Session '%s' closed", params.SessionID), nil
}

func (p *PlaywrightPlugin) closeAll(payload string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	count := len(p.sessions)

	for id, session := range p.sessions {
		if session.page != nil {
			session.page.Close()
		}
		if session.browser != nil {
			session.browser.Close()
		}
		delete(p.sessions, id)
	}

	if p.pw != nil {
		p.pw.Stop()
		p.pw = nil
	}

	return fmt.Sprintf("Closed %d session(s)", count), nil
}

func (p *PlaywrightPlugin) ariaSnapshot(payload string) (string, error) {
	var params struct {
		SessionID string `json:"session_id"`
		Selector  string `json:"selector"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		return "", fmt.Errorf("invalid payload: %w", err)
	}

	session, err := p.getSession(params.SessionID)
	if err != nil {
		return "", err
	}

	selector := params.Selector
	if selector == "" {
		selector = "body"
	}

	locator := session.page.Locator(selector)

	snapshot, err := locator.AriaSnapshot()
	if err != nil {
		return "", fmt.Errorf("aria snapshot failed: %w", err)
	}

	return snapshot, nil
}

func (p *PlaywrightPlugin) clickCoordinates(payload string) (string, error) {
	var params struct {
		SessionID string  `json:"session_id"`
		X         float64 `json:"x"`
		Y         float64 `json:"y"`
	}
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		return "", fmt.Errorf("invalid payload: %w", err)
	}

	session, err := p.getSession(params.SessionID)
	if err != nil {
		return "", err
	}

	if err := session.page.Mouse().Click(params.X, params.Y); err != nil {
		return "", fmt.Errorf("click at coordinates failed: %w", err)
	}

	return fmt.Sprintf("Clicked at coordinates (%v, %v)", params.X, params.Y), nil
}

func (p *PlaywrightPlugin) GetToolInfo(toolName string) (*squadron.ToolInfo, error) {
	info, ok := tools[toolName]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
	return info, nil
}

func (p *PlaywrightPlugin) ListTools() ([]*squadron.ToolInfo, error) {
	result := make([]*squadron.ToolInfo, 0, len(tools))
	for _, info := range tools {
		result = append(result, info)
	}
	return result, nil
}

func main() {
	squadron.Serve(&PlaywrightPlugin{})
}

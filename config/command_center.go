package config

import (
	"fmt"
	"net/url"
	"strings"
)

// CommandCenterConfig defines connection settings for a command center server.
// If no command_center block is present in config, squadron operates standalone.
//
// Host is a base URL — just the scheme + domain (and optional path prefix).
// Squadron derives the WebSocket URL by swapping http(s)→ws(s) and appending
// "/ws", and the OAuth redirect URI by preserving the scheme and appending
// "/oauth/callback". Users may include a path prefix on Host if they map
// command center behind one (e.g. "https://foo.com/commander").
type CommandCenterConfig struct {
	Host              string `hcl:"host,optional"`
	InstanceName      string `hcl:"instance_name,optional"`
	AutoReconnect     bool   `hcl:"auto_reconnect,optional"`
	ReconnectInterval int    `hcl:"reconnect_interval,optional"` // seconds

	// Deprecated: use Host. Present only so we can detect legacy configs and
	// emit a migration error. Squadron will refuse to start if this is set.
	URL string `hcl:"url,optional"`
}

// Defaults fills in default values for unset fields.
func (c *CommandCenterConfig) Defaults() {
	if c.ReconnectInterval <= 0 {
		c.ReconnectInterval = 5
	}
}

// Validate checks that required fields are set.
func (c *CommandCenterConfig) Validate() error {
	if c.URL != "" {
		return fmt.Errorf("command_center.url is deprecated; use host instead (e.g. host = \"https://mycommander.com\")")
	}
	if c.Host == "" {
		return fmt.Errorf("host is required")
	}
	u, err := url.Parse(c.Host)
	if err != nil {
		return fmt.Errorf("host is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("host must use http or https scheme (got %q)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("host must include a hostname (got %q)", c.Host)
	}
	if c.InstanceName == "" {
		return fmt.Errorf("instance_name is required")
	}
	return nil
}

// trimmedHost returns Host with a single trailing slash removed so suffix
// appends always produce a single delimiter.
func (c *CommandCenterConfig) trimmedHost() string {
	return strings.TrimSuffix(c.Host, "/")
}

// WebSocketURL returns the full WebSocket URL derived from Host by swapping
// http→ws and https→wss, then appending "/ws".
//
//	"https://foo.com"           → "wss://foo.com/ws"
//	"https://foo.com/commander" → "wss://foo.com/commander/ws"
//	"http://localhost:8080"     → "ws://localhost:8080/ws"
func (c *CommandCenterConfig) WebSocketURL() string {
	h := c.trimmedHost()
	switch {
	case strings.HasPrefix(h, "https://"):
		return "wss://" + strings.TrimPrefix(h, "https://") + "/ws"
	case strings.HasPrefix(h, "http://"):
		return "ws://" + strings.TrimPrefix(h, "http://") + "/ws"
	default:
		return h + "/ws"
	}
}

// OAuthRedirectURI returns the OAuth callback URL by preserving the Host
// scheme and appending "/oauth/callback".
//
//	"https://foo.com"           → "https://foo.com/oauth/callback"
//	"https://foo.com/commander" → "https://foo.com/commander/oauth/callback"
func (c *CommandCenterConfig) OAuthRedirectURI() string {
	return c.trimmedHost() + "/oauth/callback"
}

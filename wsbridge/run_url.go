package wsbridge

import (
	"net/url"
	"strings"
)

// RunURL links to a run on the registered command center.
// It is empty before registration or when no mission ID is supplied.
func (c *Client) RunURL(missionID string) string {
	c.mu.Lock()
	endpoint, instanceID := c.commandCenterURL, c.instanceID
	c.mu.Unlock()
	if missionID == "" || instanceID == "" {
		return ""
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return ""
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	default:
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	u.RawPath = ""
	u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/ws")
	return strings.TrimRight(u.String(), "/") + "/instances/" + url.PathEscape(instanceID) + "/runs/" + url.PathEscape(missionID)
}

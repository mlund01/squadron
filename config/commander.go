package config

import "fmt"

// CommanderConfig defines connection settings for a commander server.
// If no commander block is present in config, squadron operates standalone.
type CommanderConfig struct {
	URL                string `hcl:"url,optional"`
	InstanceName       string `hcl:"instance_name,optional"`
	AutoReconnect      bool   `hcl:"auto_reconnect,optional"`
	ReconnectInterval  int    `hcl:"reconnect_interval,optional"` // seconds
}

// Defaults fills in default values for unset fields
func (c *CommanderConfig) Defaults() {
	if c.ReconnectInterval <= 0 {
		c.ReconnectInterval = 5
	}
}

// Validate checks that required fields are set
func (c *CommanderConfig) Validate() error {
	if c.URL == "" {
		return fmt.Errorf("url is required")
	}
	if c.InstanceName == "" {
		return fmt.Errorf("instance_name is required")
	}
	return nil
}

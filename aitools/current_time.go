package aitools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// CurrentTimeTool returns the current time, optionally in a specified IANA
// timezone and Go reference-time format string.
type CurrentTimeTool struct{}

func (t *CurrentTimeTool) ToolName() string {
	return "current_time"
}

func (t *CurrentTimeTool) ToolDescription() string {
	return "Returns the current date and time. Optionally specify an IANA timezone (e.g. \"America/Chicago\", \"UTC\") and a Go reference-time format string. Defaults to UTC and RFC 3339."
}

func (t *CurrentTimeTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"timezone": {
				Type:        TypeString,
				Description: "Optional IANA timezone name (e.g. \"America/Chicago\", \"Europe/London\", \"UTC\"). Defaults to UTC.",
			},
			"format": {
				Type:        TypeString,
				Description: "Optional Go reference-time format string (e.g. \"2006-01-02 15:04:05\"). Defaults to RFC 3339.",
			},
		},
	}
}

type currentTimeParams struct {
	Timezone string `json:"timezone"`
	Format   string `json:"format"`
}

func (t *CurrentTimeTool) Call(ctx context.Context, params string) string {
	var p currentTimeParams
	if params != "" {
		if err := json.Unmarshal([]byte(params), &p); err != nil {
			return "Error: invalid parameters - " + err.Error()
		}
	}

	loc := time.UTC
	if p.Timezone != "" {
		l, err := time.LoadLocation(p.Timezone)
		if err != nil {
			return fmt.Sprintf("Error: unknown timezone %q - %s", p.Timezone, err.Error())
		}
		loc = l
	}

	layout := time.RFC3339
	if p.Format != "" {
		layout = p.Format
	}

	return time.Now().In(loc).Format(layout)
}

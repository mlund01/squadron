package aitools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// SleepTool pauses execution for a specified duration.
type SleepTool struct{}

func (t *SleepTool) ToolName() string {
	return "sleep"
}

func (t *SleepTool) ToolDescription() string {
	return "Pause execution for a specified number of seconds. Useful for waiting between API calls, polling, or rate limiting."
}

func (t *SleepTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"seconds": {
				Type:        TypeNumber,
				Description: "Number of seconds to sleep (max 300).",
			},
		},
		Required: []string{"seconds"},
	}
}

type sleepParams struct {
	Seconds float64 `json:"seconds"`
}

const maxSleepSeconds = 300

func (t *SleepTool) Call(ctx context.Context, params string) string {
	var p sleepParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.Seconds <= 0 {
		return "Error: seconds must be greater than 0"
	}
	if p.Seconds > maxSleepSeconds {
		return fmt.Sprintf("Error: maximum sleep duration is %d seconds", maxSleepSeconds)
	}

	select {
	case <-time.After(time.Duration(p.Seconds * float64(time.Second))):
		return fmt.Sprintf("Slept for %.1f seconds.", p.Seconds)
	case <-ctx.Done():
		return "Error: sleep cancelled"
	}
}

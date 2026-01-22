package aitools

import (
	"encoding/json"
	"os/exec"
)

// BashTool executes bash commands and returns the output
type BashTool struct{}

// NewBashTool creates a new Bash tool
func NewBashTool() *BashTool {
	return &BashTool{}
}

func (t *BashTool) ToolName() string {
	return "bash"
}

func (t *BashTool) ToolDescription() string {
	return "Executes a bash command and returns the output. Use this to run shell commands, scripts, or interact with the system."
}

func (t *BashTool) ToolPayloadSchema() Schema {
	return Schema{
		Type: TypeObject,
		Properties: PropertyMap{
			"command": {
				Type:        TypeString,
				Description: "The bash command to execute",
			},
		},
		Required: []string{"command"},
	}
}

type bashParams struct {
	Command string `json:"command"`
}

func (t *BashTool) Call(params string) string {
	var p bashParams
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		return "Error: invalid parameters - " + err.Error()
	}

	if p.Command == "" {
		return "Error: command is required"
	}

	cmd := exec.Command("bash", "-c", p.Command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output) + "\nError: " + err.Error()
	}

	return string(output)
}

package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var helloworldOutput string

var helloworldCmd = &cobra.Command{
	Use:   "helloworld",
	Short: "Generate a hello world workflow",
	Long:  `Creates a sample Squadron workflow that demonstrates variables, models, plugins, agents, datasets, iterators, and task dependencies.`,
	RunE:  runHelloworld,
}

func init() {
	rootCmd.AddCommand(helloworldCmd)
	helloworldCmd.Flags().StringVarP(&helloworldOutput, "output", "o", "helloworld.hcl", "Output file path")
}

const helloworldHCL = `storage {
  backend = "sqlite"
}

variable "anthropic_api_key" {
  secret = true
}

model "anthropic" {
  provider       = "anthropic"
  allowed_models = ["claude_haiku_4_5"]
  api_key        = vars.anthropic_api_key
}

plugin "pinger" {
  source  = "github.com/mlund01/plugin_pinger"
  version = "v0.0.1"
}

agent "pinger" {
  model       = models.anthropic.claude_haiku_4_5
  personality = "Friendly and enthusiastic. Loves greeting the world."
  role        = "A simple agent that uses the pinger plugin to echo greetings."
  tools       = [plugins.pinger.echo]
}

mission "hello_world" {
  directive = "Greet the world in multiple ways using the pinger plugin"
  commander {
    model = models.anthropic.claude_haiku_4_5
  }
  agents = [agents.pinger]

  dataset "names" {
    description = "Names to greet"
    schema {
      field "value" {
        type     = "string"
        required = true
      }
    }
  }

  task "create_dataset" {
    objective = <<-EOT
      Populate the 'names' dataset with exactly 3 items:
      1. value: "world"
      2. value: "earth"
      3. value: "globe"
    EOT
  }

  task "greet" {
    objective  = "Use the pinger agent's echo tool to say 'hello ${item.value}'"
    depends_on = [tasks.create_dataset]
    iterator {
      dataset  = datasets.names
      parallel = true
    }
    output {
      field "greeting" {
        type        = "string"
        description = "The greeting that was echoed"
        required    = true
      }
    }
  }

  task "summarize" {
    objective  = "Summarize what happened in the greeting task. Report each greeting that was made."
    depends_on = [tasks.greet]
    output {
      field "summary" {
        type        = "string"
        description = "A summary of all greetings"
        required    = true
      }
    }
  }
}
`

func runHelloworld(cmd *cobra.Command, args []string) error {
	output := helloworldOutput

	// Check if file already exists
	if _, err := os.Stat(output); err == nil {
		return fmt.Errorf("file already exists: %s", output)
	}

	// Ensure parent directory exists
	dir := filepath.Dir(output)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
	}

	if err := os.WriteFile(output, []byte(helloworldHCL), 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	fmt.Printf("Created %s\n", output)
	fmt.Println("\nTo run it:")
	fmt.Println("  squadron vars set anthropic_api_key <your-key>")
	fmt.Printf("  squadron mission -c %s hello_world\n", output)
	return nil
}

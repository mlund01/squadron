package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"squadron/config"
	"squadron/config/vault"
)

type providerInfo struct {
	Name     string // display name
	Provider string // HCL provider value
	VarName  string // variable name for API key
	ModelKey string // cheapest model key for starter mission
}

var providers = []providerInfo{
	{
		Name:     "Anthropic (Claude)",
		Provider: "anthropic",
		VarName:  "anthropic_api_key",
		ModelKey: "claude_haiku_4_5",
	},
	{
		Name:     "OpenAI (GPT)",
		Provider: "openai",
		VarName:  "openai_api_key",
		ModelKey: "gpt_4_1_mini",
	},
	{
		Name:     "Google (Gemini)",
		Provider: "gemini",
		VarName:  "gemini_api_key",
		ModelKey: "gemini_2_5_flash_lite",
	},
}

var quickstartCmd = &cobra.Command{
	Use:   "quickstart",
	Short: "Interactive setup wizard for new Squadron projects",
	Long: `Walk through setting up a new Squadron project interactively.
Configures your LLM provider, stores your API key securely, and generates
a starter mission that demonstrates Squadron's core features.`,
	Run: func(cmd *cobra.Command, args []string) {
		if warning, verr := validateConfigDir("."); verr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", verr)
			os.Exit(1)
		} else if warning != "" {
			fmt.Fprintf(os.Stderr, "Warning: %s\n\n", warning)
			if !promptYesNo("Continue anyway?") {
				return
			}
		}
		dir, err := RunQuickstart(".")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\nSquadron configured at %s\n", dir)
		fmt.Println("\nTo run the starter mission:")
		fmt.Printf("  squadron mission -c %s hn_research\n", dir)
		fmt.Println("\nTo start the command center:")
		fmt.Printf("  squadron engage -c %s\n", dir)
	},
}

func init() {
	rootCmd.AddCommand(quickstartCmd)
}

// RunQuickstart runs the interactive setup wizard.
// Returns the config directory path where files were generated.
func RunQuickstart(configPath string) (string, error) {
	reader := bufio.NewReader(os.Stdin)

	// Welcome
	printBanner()
	fmt.Println()
	fmt.Println("  Let's get you set up.")
	fmt.Println()

	// Step 1: Choose provider
	fmt.Println("Which LLM provider would you like to use?")
	fmt.Println()
	for i, p := range providers {
		fmt.Printf("  %d. %s\n", i+1, p.Name)
	}
	fmt.Println()

	provider := promptChoice(reader, "Choose provider", len(providers))
	chosen := providers[provider]
	fmt.Println()

	// Step 2: API key
	fmt.Printf("Enter your %s API key:\n", chosen.Name)
	fmt.Printf("  (You can find this at your provider's dashboard)\n")
	fmt.Println()

	apiKey, err := promptSecret(reader, "API key")
	if err != nil {
		return "", fmt.Errorf("could not read API key: %w", err)
	}
	fmt.Println()

	// Step 3: Starter mission
	fmt.Println("Include a starter mission? (hn_research)")
	fmt.Println("  Fetches Hacker News, researches top stories, creates a summary.")
	fmt.Println()
	includeStarter := promptYesNo("Include starter mission?")
	fmt.Println()

	// Step 4: Generate config files (before init so abort leaves no .squadron/)
	configDir := configPath
	if configDir == "." {
		configDir, _ = os.Getwd()
	}

	if err := generateStarterConfig(configDir, chosen, includeStarter); err != nil {
		return "", fmt.Errorf("could not generate config: %w", err)
	}

	// Step 5: Initialize vault and store API key
	if err := RunInit("", vault.ProviderFile); err != nil {
		return "", fmt.Errorf("initialization failed: %w", err)
	}

	if err := config.SetVar(chosen.VarName, apiKey); err != nil {
		return "", fmt.Errorf("could not store API key: %w", err)
	}

	fmt.Println("  Configuration complete:")
	fmt.Printf("    %s/squadron.hcl\n", configDir)
	fmt.Println("  API key stored in encrypted vault.")
	if includeStarter {
		fmt.Println()
		fmt.Println("  Starter mission (hn_research):")
		fmt.Println("    1. Fetches the Hacker News front page")
		fmt.Println("    2. Researches the top 3 stories in parallel")
		fmt.Println("    3. Creates an executive summary")
	}

	return configDir, nil
}

// promptChoice asks the user to pick a numbered option. Returns 0-indexed.
func promptChoice(reader *bufio.Reader, prompt string, max int) int {
	for {
		fmt.Printf("%s [1-%d]: ", prompt, max)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if len(line) == 1 {
			n := int(line[0] - '0')
			if n >= 1 && n <= max {
				return n - 1
			}
		}
		fmt.Printf("  Please enter a number between 1 and %d.\n", max)
	}
}

// promptSecret reads a secret value with echo disabled.
func promptSecret(reader *bufio.Reader, prompt string) (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Printf("%s: ", prompt)
		password, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println() // newline after hidden input
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(password)), nil
	}
	// Non-terminal fallback
	fmt.Printf("%s: ", prompt)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// generateStarterConfig writes the starter HCL config file.
func generateStarterConfig(dir string, p providerInfo, includeStarter bool) error {
	hcl := baseHCL(p, includeStarter)
	if includeStarter {
		hcl += "\n" + starterMissionHCL(p)
	}

	path := filepath.Join(dir, "squadron.hcl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("file already exists: %s", path)
		}
		return err
	}
	defer f.Close()
	_, err = f.WriteString(hcl)
	return err
}

// baseHCL generates the provider and model config, plus the researcher agent
// when the starter mission is included.
func baseHCL(p providerInfo, includeStarter bool) string {
	base := fmt.Sprintf(`variable "%s" {
  secret = true
}

model "%s" {
  provider = "%s"
  api_key  = vars.%s
}
`, p.VarName, p.Provider, p.Provider, p.VarName)

	if !includeStarter {
		return base
	}

	return base + fmt.Sprintf(`
agent "researcher" {
  model       = models.%s.%s
  personality = "You are a precise web researcher. Always use the HTTP GET tool to fetch web pages rather than relying on your own knowledge. Extract information exactly as requested from the HTML responses."
  role        = "Web researcher that fetches and analyzes web content"
  tools       = [builtins.http.get]
}
`, p.Provider, p.ModelKey)
}

// starterMissionHCL generates the HN research starter mission.
func starterMissionHCL(p providerInfo) string {
	return fmt.Sprintf(`mission "hn_research" {
  directive = "Research the top stories on Hacker News"

  commander {
    model = models.%s.%s
  }

  agents = [agents.researcher]

  dataset "stories" {
    description = "Top stories from Hacker News"
    schema {
      field "title" {
        type     = "string"
        required = true
      }
      field "url" {
        type     = "string"
        required = true
      }
    }
  }

  task "discover" {
    objective = <<-EOT
      Use HTTP GET to fetch https://news.ycombinator.com.
      Parse the HTML response to find the top 3 stories on the front page.
      For each story, extract its title and URL, then add it to the 'stories' dataset
      using the dataset_set tool.
    EOT
  }

  task "research" {
    depends_on = [tasks.discover]
    iterator {
      dataset  = datasets.stories
      parallel = true
    }
    objective = <<-EOT
      Use HTTP GET to fetch the article at ${item.url}.
      Read the content and extract key information.
      Summarize the article and identify key takeaways.
    EOT
    output {
      field "title" {
        type        = "string"
        required    = true
        description = "Title of the article"
      }
      field "summary" {
        type        = "string"
        required    = true
        description = "2-3 sentence summary of the article"
      }
      field "key_points" {
        type        = "list"
        required    = true
        description = "3-5 key takeaways from the article"
      }
    }
  }

  task "summarize" {
    depends_on = [tasks.research]
    objective  = <<-EOT
      Create a brief executive summary covering all researched articles.
      Highlight common themes, the most interesting findings, and why
      these stories are trending on Hacker News.
    EOT
    output {
      field "report" {
        type        = "string"
        required    = true
        description = "Executive summary covering all articles"
      }
    }
  }
}
`, p.Provider, p.ModelKey)
}

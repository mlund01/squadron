package prompts

import (
	_ "embed"
	"fmt"
	"strings"

	"squadron/aitools"
	"squadron/config"
)

//go:embed agent.md
var agentPromptTemplate string

//go:embed commander.md
var commanderPromptTemplate string

// SkillInfo contains name and description for an available skill (passed to prompts)
type SkillInfo struct {
	Name        string
	Description string
}

// GetAgentPrompt returns the agent system prompt with mode, secrets, and skills injected.
// Tools are no longer included in the prompt — they are passed via the API's tool definitions.
func GetAgentPrompt(mode config.AgentMode, secrets []SecretInfo, skills []SkillInfo) string {
	prompt := agentPromptTemplate

	// Inject secrets section
	secretsSection := formatSecretsSection(secrets)
	prompt = strings.Replace(prompt, "{{SECRETS}}", secretsSection, 1)

	// Inject skills section
	skillsSection := formatSkillsSection(skills)
	prompt = strings.Replace(prompt, "{{SKILLS}}", skillsSection, 1)

	// Inject mode instructions
	modeInstructions := getModeInstructions(mode)
	prompt = strings.Replace(prompt, "{{MODE_INSTRUCTIONS}}", modeInstructions, 1)

	// Inject response patterns based on mode
	responsePatterns := getResponsePatterns(mode)
	prompt = strings.Replace(prompt, "{{RESPONSE_PATTERNS}}", responsePatterns, 1)

	// Inject rules based on mode
	rules := getRules(mode)
	prompt = strings.Replace(prompt, "{{RULES}}", rules, 1)

	return prompt
}

// formatSkillsSection formats the available skills for the prompt
func formatSkillsSection(skills []SkillInfo) string {
	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Skills\n\n")
	sb.WriteString("Use `load_skill` to activate a skill when its description matches your needs:\n\n")
	for _, s := range skills {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", s.Name, s.Description))
	}
	return sb.String()
}

// formatSecretsSection formats the secrets info for the prompt
func formatSecretsSection(secrets []SecretInfo) string {
	if len(secrets) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Secrets\n\n")
	sb.WriteString("Use `${secrets.<name>}` in tool input JSON. Values are injected at runtime. Never output actual secret values.\n\n")
	for _, s := range secrets {
		if s.Description != "" {
			sb.WriteString(fmt.Sprintf("- `${secrets.%s}` - %s\n", s.Name, s.Description))
		} else {
			sb.WriteString(fmt.Sprintf("- `${secrets.%s}`\n", s.Name))
		}
	}
	return sb.String()
}

// getModeInstructions returns instructions based on agent mode
func getModeInstructions(mode config.AgentMode) string {
	switch mode {
	case config.ModeMission:
		return `**MISSION MODE:** You are running as part of an automated mission.
- Use REASONING before every action or answer. Continue until the task is fully complete.
- Only provide an ANSWER when done. Be autonomous — make reasonable assumptions.`

	case config.ModeChat:
		fallthrough
	default:
		return `**CHAT MODE:** You are chatting interactively with a user.
- REASONING is optional - use it when helpful for complex tasks
- You may ask clarifying questions if the request is ambiguous
- Respond conversationally and helpfully`
	}
}

// getResponsePatterns returns the response patterns based on mode
func getResponsePatterns(mode config.AgentMode) string {
	var sb strings.Builder

	if mode == config.ModeMission {
		sb.WriteString(`**Working:** Use REASONING tags, then call a tool via function calling.
**Done:** Use REASONING tags, then ANSWER tags with your final result.
**Need info:** Use ` + "`ask_commander`" + ` — only when critical details are missing.

## Commander Responses

- ` + "`<COMMANDER_RESPONSE>`" + `: Answer to your question. Continue your task using this information.
- ` + "`<NEW_TASK>`" + `: New assignment. **Drop in-flight work** and start fresh on this task.`)
	} else {
		sb.WriteString(`**Direct answer:** Wrap response in ANSWER tags.
**Complex question:** Use REASONING tags, then ANSWER tags.
**Tool needed:** Use REASONING tags, then call the tool. Never output raw text before a tool call.`)
	}

	return sb.String()
}

// getRules returns rules based on mode
func getRules(mode config.AgentMode) string {
	var rules []string

	if mode == config.ModeMission {
		rules = append(rules, "**Always reason first.** Every response MUST start with a REASONING block.")
		rules = append(rules, "**Complete the task.** Keep working (REASONING → tool call) until the task is done.")
		rules = append(rules, "**ANSWER means done.** Only use ANSWER when the entire task is complete.")
		rules = append(rules, "**Be autonomous.** Don't ask questions - make reasonable assumptions and proceed.")
	} else {
		rules = append(rules, "**All text in tags.** Never output raw text outside of tags. Any explanation before a tool call goes in REASONING.")
		rules = append(rules, "**Reasoning is optional.** Use REASONING when it helps, skip it for simple responses.")
		rules = append(rules, "**Be conversational.** You may ask clarifying questions if needed.")
	}

	rules = append(rules, "**Tools are optional.** Only use tools when you need information you don't have or capabilities you lack.")
	rules = append(rules, "**Handle errors gracefully.** If a tool call fails, reason about why and try a different approach.")
	rules = append(rules, "**Keep responses concise.** Each response has a ~16,000 token output limit. Keep reasoning brief and avoid producing extremely long content in a single response.")

	var sb strings.Builder
	for i, rule := range rules {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, rule))
	}
	return sb.String()
}

// AgentInfo represents basic info about an agent for the commander prompt
type AgentInfo struct {
	Name        string
	Description string
}

// SecretInfo contains name and description for a secret (passed to prompts)
type SecretInfo struct {
	Name        string
	Description string
}

// IterationOptions contains info about task iteration for conditional prompt content
type IterationOptions struct {
	IsIteration bool // Whether this is an iterated task
	IsParallel  bool // If iteration, whether running in parallel (vs sequential)
}

// GetCommanderPrompt returns the commander system prompt with available agents injected
func GetCommanderPrompt(agents []AgentInfo, iterOpts IterationOptions) string {
	prompt := commanderPromptTemplate

	// Inject agents
	agentsDescription := formatAgents(agents)
	prompt = strings.Replace(prompt, "{{AGENTS}}", agentsDescription, 1)

	// Inject iteration-specific content conditionally
	parallelContent := ""
	sequentialContent := ""

	if iterOpts.IsIteration {
		if iterOpts.IsParallel {
			parallelContent = getParallelIterationContent()
		} else {
			sequentialContent = getSequentialIterationContent()
		}
	}

	prompt = strings.Replace(prompt, "{{PARALLEL_ITERATION_CONTEXT}}", parallelContent, 1)
	prompt = strings.Replace(prompt, "{{SEQUENTIAL_ITERATION_CONTEXT}}", sequentialContent, 1)

	return prompt
}

// getParallelIterationContent returns content about reusing questions from other iterations
func getParallelIterationContent() string {
	return `## Parallel Iteration: Shared Questions

Other iterations may have already asked questions to dependency commanders. Before using ` + "`ask_commander`" + `, check ` + "`list_commander_questions`" + ` for existing questions and use ` + "`get_commander_answer`" + ` to retrieve cached answers instead of asking again.

`
}

// getSequentialIterationContent returns content about learnings for sequential iterations
func getSequentialIterationContent() string {
	return `## Sequential Iteration: Learnings

Review any "Learnings from Previous Iteration" in your context and apply them. When completing your task, include a ` + "`<LEARNINGS>`" + ` block to help the next iteration:

` + "```" + `
<LEARNINGS>
{
  "key_insights": ["Useful observations"],
  "failures": [{"problem": "What went wrong", "solution": "How it was fixed"}],
  "recommendations": "Advice for the next iteration"
}
</LEARNINGS>
` + "```" + `

`
}

// formatAgents formats the agents list into a readable string for the prompt
func formatAgents(agents []AgentInfo) string {
	if len(agents) == 0 {
		return "NO AGENTS AVAILABLE"
	}

	var sb strings.Builder
	for _, agent := range agents {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", agent.Name, agent.Description))
	}

	return sb.String()
}

// FormatFolderContext builds a system prompt section describing available folders.
func FormatFolderContext(store aitools.FolderStore) string {
	if store == nil {
		return ""
	}
	infos := store.FolderInfos()
	if len(infos) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Folders\n\n")
	sb.WriteString("You have access to file folders via the file_list, file_read, file_create, file_delete, file_search, and file_grep tools.\n")
	sb.WriteString("Use the `folder` parameter to specify which folder. ")

	defaultFolder := store.DefaultFolder()
	if defaultFolder != "" {
		sb.WriteString(fmt.Sprintf("Omit it to use the default folder (%q).\n\n", defaultFolder))
	} else {
		sb.WriteString("The `folder` parameter is required since no default folder is configured.\n\n")
	}

	for _, info := range infos {
		access := "read-only"
		if info.Writable {
			access = "read/write"
		}
		label := ""
		if info.IsDedicated {
			label = " (dedicated mission folder)"
		}
		desc := ""
		if info.Description != "" {
			desc = " — " + info.Description
		}
		sb.WriteString(fmt.Sprintf("- **%s**%s%s (%s)\n", info.Name, label, desc, access))
	}

	return sb.String()
}

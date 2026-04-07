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
	sb.WriteString("You can load specialized skills on-demand using the `load_skill` tool. ")
	sb.WriteString("Each skill provides additional tools and detailed instructions for specific tasks.\n\n")
	sb.WriteString("**Load a skill when its description matches what you need to do:**\n\n")
	for _, s := range skills {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", s.Name, s.Description))
	}
	sb.WriteString("\nUse `load_skill` with the skill name to activate it. Once loaded, follow the skill's instructions and use its newly available tools.\n")
	return sb.String()
}

// formatSecretsSection formats the secrets info for the prompt
func formatSecretsSection(secrets []SecretInfo) string {
	if len(secrets) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Secrets\n\n")
	sb.WriteString("Use `${secrets.<name>}` in tool input JSON. The actual values will be injected at runtime.\n\n")
	sb.WriteString("**Available:**\n")
	for _, s := range secrets {
		if s.Description != "" {
			sb.WriteString(fmt.Sprintf("- `${secrets.%s}` - %s\n", s.Name, s.Description))
		} else {
			sb.WriteString(fmt.Sprintf("- `${secrets.%s}`\n", s.Name))
		}
	}
	sb.WriteString("\n**Important:** Never output actual secret values. Always use the placeholder syntax.\n")
	return sb.String()
}

// getModeInstructions returns instructions based on agent mode
func getModeInstructions(mode config.AgentMode) string {
	switch mode {
	case config.ModeMission:
		return `**MISSION MODE:** You are running as part of an automated mission. You have been given a task to complete.
- You MUST use REASONING before every action or answer
- Continue cycling through REASONING and ACTION until the task is fully complete
- Only provide an ANSWER when the task is done
- Be thorough and autonomous - do not ask clarifying questions, make reasonable assumptions`

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
		sb.WriteString(`### Pattern 1: Reasoning + Tool Call (continue working)
Put your reasoning in REASONING tags, then call the appropriate tool via function calling.

` + "```" + `
<REASONING>
Analyze the current state and what needs to be done next...
</REASONING>
[then call the tool via function calling]
` + "```" + `

### Pattern 2: Reasoning + Answer (task complete)
Use this ONLY when the task is fully complete.

` + "```" + `
<REASONING>
The task is complete because...
</REASONING>
<ANSWER>
Summary of what was accomplished and the final result.
</ANSWER>
` + "```" + `

### Pattern 3: Ask Commander for Clarification
When you need more information from the commander, use the ` + "`ask_commander`" + ` tool.
**Only ask when truly necessary. Make reasonable assumptions when possible, but ask if critical details are missing.**

### Pattern 4: Multi-step Reasoning
For complex analysis, you may use multiple REASONING blocks before making a tool call.

## Commander Responses

When you ask the commander a question via ask_commander, you may receive one of two responses:

### ` + "`<COMMANDER_RESPONSE>`" + ` - Answer to Your Question

The commander is providing the information you requested. Continue your task from where you left off using this new information.

### ` + "`<NEW_TASK>`" + ` - New Assignment

The commander has decided to give you a different task instead. **Ignore any in-flight work** and start fresh on this new task. Treat it as a completely new assignment.`)
	} else {
		// Chat mode
		sb.WriteString(`### Pattern 1: Direct Answer
Use this when you can answer without tools:

` + "```" + `
<ANSWER>
Your response to the user
</ANSWER>
` + "```" + `

### Pattern 2: Reasoning + Answer
Use this for complex questions that benefit from showing your thought process:

` + "```" + `
<REASONING>
Your reasoning about the situation
</REASONING>
<ANSWER>
Your response to the user
</ANSWER>
` + "```" + `

### Pattern 3: Tool Call
When you need to use a tool, put reasoning in REASONING tags then call the tool via function calling.
**Any explanation MUST be inside REASONING tags — never output raw text before a tool call.**`)
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
	return `## Reusing Questions from Other Iterations

When running as part of an iterated task, other iterations may have already asked questions to dependency commanders. You can reuse their answers to avoid redundant queries.

### Listing Asked Questions

Use ` + "`list_commander_questions`" + ` to see what questions have been asked. It returns a numbered list of questions (without answers).

### Getting Cached Answers

Use ` + "`get_commander_answer`" + ` with the question index to get the cached answer. If the answer is still being fetched by another iteration, this will wait until it's ready.

**Tip:** Check ` + "`list_commander_questions`" + ` first before using ` + "`ask_commander`" + `. If another iteration already asked a similar question, use ` + "`get_commander_answer`" + ` instead to avoid duplicate work.

`
}

// getSequentialIterationContent returns content about learnings for sequential iterations
func getSequentialIterationContent() string {
	return `## Learnings for Next Iteration

When commanding a sequential iteration, you may receive learnings from the previous iteration. These contain insights, failure solutions, and recommendations that can help you succeed.

**Applying Learnings:**
- Review any "Learnings from Previous Iteration" in your context
- Apply insights to avoid repeating mistakes
- Leverage successful strategies mentioned

**Capturing Learnings:**
When completing your task, include a ` + "`<LEARNINGS>`" + ` block after your ANSWER to help the next iteration:

` + "```" + `
<ANSWER>
[Your answer here]
</ANSWER>
<LEARNINGS>
{
  "key_insights": ["Useful observations for similar problems"],
  "failures": [{"problem": "What went wrong", "solution": "How it was fixed"}],
  "recommendations": "Advice for the next iteration"
}
</LEARNINGS>
` + "```" + `

Include learnings when you or your agents:
- Discovered unexpected behavior or edge cases
- Encountered and solved problems
- Identified optimizations or better approaches
- Have context that would otherwise be lost between iterations

`
}

// formatAgents formats the agents list into a readable string for the prompt
func formatAgents(agents []AgentInfo) string {
	if len(agents) == 0 {
		return "NO AGENTS AVAILABLE"
	}

	var sb strings.Builder
	sb.WriteString("### call_agent\n\n")
	sb.WriteString("Call an agent to perform a task or respond to an agent's question.\n\n")
	sb.WriteString("**Input Schema:**\n```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"name\": \"string (required) - The name of the agent to call\",\n")
	sb.WriteString("  \"task\": \"string - A new task for the agent. Always treated as a fresh assignment.\",\n")
	sb.WriteString("  \"response\": \"string - Response to an agent's ASK_COMMANDER question. Agent continues from where it left off.\"\n")
	sb.WriteString("}\n```\n\n")
	sb.WriteString("Provide exactly one of `task` or `response`, not both.\n\n")
	sb.WriteString("**Available agents:**\n\n")

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

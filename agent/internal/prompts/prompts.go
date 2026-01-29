package prompts

import (
	_ "embed"
	"fmt"
	"strings"

	"squad/aitools"
	"squad/config"
)

//go:embed agent.md
var agentPromptTemplate string

//go:embed supervisor.md
var supervisorPromptTemplate string

// GetAgentPrompt returns the agent system prompt with tools and mode injected
func GetAgentPrompt(tools map[string]aitools.Tool, mode config.AgentMode) string {
	prompt := agentPromptTemplate

	// Inject tools
	toolsDescription := formatTools(tools)
	prompt = strings.Replace(prompt, "{{TOOLS}}", toolsDescription, 1)

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

// getModeInstructions returns instructions based on agent mode
func getModeInstructions(mode config.AgentMode) string {
	switch mode {
	case config.ModeWorkflow:
		return `**WORKFLOW MODE:** You are running as part of an automated workflow. You have been given a task to complete.
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

	if mode == config.ModeWorkflow {
		sb.WriteString(`### Pattern 1: Reasoning + Tool Call (continue working)
Use this when you need to perform an action to complete the task.
**Output ___STOP___ after ACTION_INPUT and wait for the result.**

` + "```" + `
<REASONING>
Analyze the current state and what needs to be done next...
</REASONING>
<ACTION>tool_name</ACTION>
<ACTION_INPUT>{"param": "value"}</ACTION_INPUT>___STOP___
` + "```" + `

### Pattern 2: Reasoning + Answer (task complete)
Use this ONLY when the task is fully complete.
**Output ___STOP___ after ANSWER to signal completion.**

` + "```" + `
<REASONING>
The task is complete because...
</REASONING>
<ANSWER>
Summary of what was accomplished and the final result.
</ANSWER>___STOP___
` + "```" + `

### Pattern 3: Multi-step Reasoning
For complex analysis, you may use multiple REASONING blocks:

` + "```" + `
<REASONING>
First, analyzing the problem...
</REASONING>
<REASONING>
Based on that analysis, the next step is...
</REASONING>
<ACTION>tool_name</ACTION>
<ACTION_INPUT>{"param": "value"}</ACTION_INPUT>___STOP___
` + "```")
	} else {
		// Chat mode
		sb.WriteString(`### Pattern 1: Direct Answer
Use this when you can answer without tools:

` + "```" + `
<ANSWER>
Your response to the user
</ANSWER>___STOP___
` + "```" + `

### Pattern 2: Reasoning + Answer
Use this for complex questions that benefit from showing your thought process:

` + "```" + `
<REASONING>
Your reasoning about the situation
</REASONING>
<ANSWER>
Your response to the user
</ANSWER>___STOP___
` + "```" + `

### Pattern 3: Tool Call
Use this when you need to use a tool. **Any explanation of what you're doing MUST be inside REASONING tags.**
**Output ___STOP___ after ACTION_INPUT and wait for the result.**

` + "```" + `
<REASONING>
Explaining what you're about to do and why...
</REASONING>
<ACTION>tool_name</ACTION>
<ACTION_INPUT>{"param": "value"}</ACTION_INPUT>___STOP___
` + "```" + `

**WRONG - never do this:**
` + "```" + `
I'll help you by using the tool...
<ACTION>tool_name</ACTION>
` + "```")
	}

	return sb.String()
}

// getRules returns rules based on mode
func getRules(mode config.AgentMode) string {
	var rules []string

	if mode == config.ModeWorkflow {
		rules = append(rules, "**Always reason first.** Every response MUST start with a REASONING block.")
		rules = append(rules, "**Complete the task.** Keep working (REASONING → ACTION) until the task is done.")
		rules = append(rules, "**One action per turn.** After ACTION_INPUT, stop and wait for OBSERVATION.")
		rules = append(rules, "**ANSWER means done.** Only use ANSWER when the entire task is complete.")
		rules = append(rules, "**Be autonomous.** Don't ask questions - make reasonable assumptions and proceed.")
	} else {
		rules = append(rules, "**All text in tags.** Never output raw text outside of tags. Any explanation before a tool call goes in REASONING.")
		rules = append(rules, "**Reasoning is optional.** Use REASONING when it helps, skip it for simple responses.")
		rules = append(rules, "**One pattern per turn.** Either provide an ANSWER or request a tool call, never both.")
		rules = append(rules, "**Be conversational.** You may ask clarifying questions if needed.")
	}

	rules = append(rules, "**Stop after ACTION_INPUT.** Do not generate OBSERVATION yourself. Wait for the system to provide it.")
	rules = append(rules, "**Tools are optional.** Only use tools when you need information you don't have or capabilities you lack.")
	rules = append(rules, "**Handle errors gracefully.** If an action fails, reason about why and try a different approach.")

	var sb strings.Builder
	for i, rule := range rules {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, rule))
	}
	return sb.String()
}

// AgentInfo represents basic info about an agent for the supervisor prompt
type AgentInfo struct {
	Name        string
	Description string
}

// GetSupervisorPrompt returns the supervisor system prompt with available agents injected
func GetSupervisorPrompt(agents []AgentInfo) string {
	prompt := supervisorPromptTemplate

	// Inject agents
	agentsDescription := formatAgents(agents)
	prompt = strings.Replace(prompt, "{{AGENTS}}", agentsDescription, 1)

	return prompt
}

// formatAgents formats the agents list into a readable string for the prompt
func formatAgents(agents []AgentInfo) string {
	if len(agents) == 0 {
		return "NO AGENTS AVAILABLE"
	}

	var sb strings.Builder
	sb.WriteString("### call_agent\n\n")
	sb.WriteString("Call another agent to perform a subtask.\n\n")
	sb.WriteString("**Input Schema:**\n```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"name\": \"string (required) - The name of the agent to call\",\n")
	sb.WriteString("  \"task\": \"string (required) - The task description for the agent\"\n")
	sb.WriteString("}\n```\n\n")
	sb.WriteString("**Available agents:**\n\n")

	for _, agent := range agents {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", agent.Name, agent.Description))
	}

	return sb.String()
}

// formatTools formats the tools map into a readable string for the prompt
func formatTools(tools map[string]aitools.Tool) string {
	if len(tools) == 0 {
		return "NO TOOLS AVAILABLE"
	}

	var sb strings.Builder
	for toolName, tool := range tools {
		sb.WriteString(fmt.Sprintf("### %s\n\n", toolName))
		sb.WriteString(fmt.Sprintf("%s\n\n", tool.ToolDescription()))
		sb.WriteString(fmt.Sprintf("**Input Schema:**\n```json\n%s\n```\n\n", tool.ToolPayloadSchema().String()))
	}
	return sb.String()
}

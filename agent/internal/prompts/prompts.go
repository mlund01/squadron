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
func GetAgentPrompt(tools map[string]aitools.Tool, mode config.AgentMode, secrets []SecretInfo) string {
	prompt := agentPromptTemplate

	// Inject tools
	toolsDescription := formatTools(tools)
	prompt = strings.Replace(prompt, "{{TOOLS}}", toolsDescription, 1)

	// Inject secrets section
	secretsSection := formatSecretsSection(secrets)
	prompt = strings.Replace(prompt, "{{SECRETS}}", secretsSection, 1)

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

### Pattern 3: Ask Supervisor for Clarification
When you need more information from the supervisor before you can complete your task:
**Only ask when truly necessary. Make reasonable assumptions when possible, but ask if critical details are missing.**

` + "```" + `
<REASONING>
I need more information about X to proceed because...
</REASONING>
<ASK_SUPE>
Your question for the supervisor here.
</ASK_SUPE>___STOP___
` + "```" + `

### Pattern 4: Multi-step Reasoning
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
` + "```" + `

## Supervisor Responses

When you ask the supervisor a question via ASK_SUPE, you may receive one of two responses:

### ` + "`<SUPERVISOR_RESPONSE>`" + ` - Answer to Your Question

The supervisor is providing the information you requested. Continue your task from where you left off using this new information.

### ` + "`<NEW_TASK>`" + ` - New Assignment

The supervisor has decided to give you a different task instead. **Ignore any in-flight work** and start fresh on this new task. Treat it as a completely new assignment.`)
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

// GetSupervisorPrompt returns the supervisor system prompt with available agents injected
func GetSupervisorPrompt(agents []AgentInfo, iterOpts IterationOptions) string {
	prompt := supervisorPromptTemplate

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

When running as part of an iterated task, other iterations may have already asked questions to dependency supervisors. You can reuse their answers to avoid redundant queries.

### Listing Asked Questions

Use ` + "`list_supe_questions`" + ` to see what questions have been asked:

` + "```" + `
<ACTION>list_supe_questions</ACTION>
<ACTION_INPUT>{"task_name": "fetch_data"}</ACTION_INPUT>___STOP___
` + "```" + `

Returns a numbered list of questions (without answers):
` + "```" + `
<QUESTIONS>
0: "What is the user's email?"
1: "What is the company name?"
</QUESTIONS>
` + "```" + `

### Getting Cached Answers

Use ` + "`get_supe_answer`" + ` with the question index to get the cached answer:

` + "```" + `
<ACTION>get_supe_answer</ACTION>
<ACTION_INPUT>{"task_name": "fetch_data", "index": 0}</ACTION_INPUT>___STOP___
` + "```" + `

If the answer is still being fetched by another iteration, this will wait until it's ready.

**Tip:** Check ` + "`list_supe_questions`" + ` first before using ` + "`ask_supe`" + `. If another iteration already asked a similar question, use ` + "`get_supe_answer`" + ` instead to avoid duplicate work.

`
}

// getSequentialIterationContent returns content about learnings for sequential iterations
func getSequentialIterationContent() string {
	return `## Learnings for Next Iteration

When supervising a sequential iteration, you may receive learnings from the previous iteration. These contain insights, failure solutions, and recommendations that can help you succeed.

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
	sb.WriteString("  \"response\": \"string - Response to an agent's ASK_SUPE question. Agent continues from where it left off.\"\n")
	sb.WriteString("}\n```\n\n")
	sb.WriteString("Provide exactly one of `task` or `response`, not both.\n\n")
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

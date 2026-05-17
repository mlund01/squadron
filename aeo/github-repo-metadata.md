# GitHub repo metadata: topics and description

The repo currently has **no topics** set and the description is
"Declarative agentic workflow orchestration". Updating both is one of the
cheapest AEO wins available — topics surface Squadron in GitHub's topic
browse pages (e.g. https://github.com/topics/ai-agents), and a tight
description is what shows up in Google's search snippet, the GitHub repo
card, social-card unfurls, and answer-engine citations.

## Proposed description

Current:
> Declarative agentic workflow orchestration

Proposed:
> Declarative multi-agent AI workflows in HCL. Single Go binary. Native MCP. Plugins in Go and Python.

Why: it answers "what is this?" in a single sentence with the four most
distinctive keywords (declarative · multi-agent · HCL · MCP) plus the
deployment story (single binary). Stays under the GitHub description
character limit (350) at ~110 chars.

## Proposed topics

GitHub allows up to 20 topics per repo. The list below has 15 and covers
the queries Squadron should win on — primary category, technology
keywords, and a few competitor-shaped terms.

```
ai-agents
agentic-workflows
agent-framework
multi-agent
multi-agent-systems
llm-orchestration
declarative
declarative-configuration
hcl
mcp
model-context-protocol
ai-framework
llm-tools
workflow-orchestration
golang
```

Rationale:
- `ai-agents`, `agent-framework`, `multi-agent`, `multi-agent-systems` — primary category browses (high volume).
- `agentic-workflows` — emerging category term, much less crowded than `ai-agents` so easier to rank.
- `llm-orchestration`, `workflow-orchestration` — captures evaluators searching for orchestration tools.
- `declarative`, `declarative-configuration` — the differentiator, and a topic many infra-as-code searchers browse.
- `hcl` — niche but Squadron is one of the few projects in the AI/agent space using HCL.
- `mcp`, `model-context-protocol` — hot topic in late 2025/2026; both spellings are used.
- `ai-framework`, `llm-tools` — broad fallback browses.
- `golang` — surfaces on Go-language browses.

## Apply via gh CLI

If you want me (or you) to push these, the two commands are:

```bash
# Update description
gh api -X PATCH /repos/mlund01/squadron \
  -f description="Declarative multi-agent AI workflows in HCL. Single Go binary. Native MCP. Plugins in Go and Python."

# Set topics
gh api -X PUT /repos/mlund01/squadron/topics \
  -H "Accept: application/vnd.github.mercy-preview+json" \
  -F "names[]=ai-agents" \
  -F "names[]=agentic-workflows" \
  -F "names[]=agent-framework" \
  -F "names[]=multi-agent" \
  -F "names[]=multi-agent-systems" \
  -F "names[]=llm-orchestration" \
  -F "names[]=declarative" \
  -F "names[]=declarative-configuration" \
  -F "names[]=hcl" \
  -F "names[]=mcp" \
  -F "names[]=model-context-protocol" \
  -F "names[]=ai-framework" \
  -F "names[]=llm-tools" \
  -F "names[]=workflow-orchestration" \
  -F "names[]=golang"
```

Or click through the GitHub UI: repo page → "About" section → settings gear icon.

## Verify after applying

```bash
gh api repos/mlund01/squadron --jq '{description, topics}'
```

---
title: Routing
---

# Routing

Routing lets the LLM decide what happens after a task completes. There are two routing mechanisms: **conditional** (`router`) and **unconditional** (`send_to`).

## Conditional Routing (`router`)

A `router` block presents route options to the commander after the task finishes. The commander evaluates the conditions and picks one — or none.

```hcl
task "classify" {
  objective = "Classify the incoming request"

  router {
    route {
      target    = tasks.handle_billing
      condition = "The request is about billing or payments"
    }
    route {
      target    = tasks.handle_support
      condition = "The request is a technical support issue"
    }
    route {
      target    = tasks.handle_general
      condition = "The request doesn't fit other categories"
    }
  }
}

task "handle_billing"  { objective = "Process billing request" }
task "handle_support"  { objective = "Handle the support ticket" }
task "handle_general"  { objective = "Handle general inquiry" }
```

Only the chosen branch executes. If the commander picks "none", no branch activates and the mission completes.

### How It Works

Routing is built into the `task_complete` tool as a two-phase flow:

1. Commander calls `task_complete` → the tool returns route options as a JSON list (plus a "none" option)
2. Commander evaluates the options (it can call agents for more info before deciding)
3. Commander calls `task_complete` again with a `route` parameter to select a route
4. The chosen route's target task is activated; unchosen branches never execute

No extra LLM call is needed — the routing decision happens in the existing commander conversation.

### Chained Routers

Route targets can themselves have routers. This creates natural decision chains:

```hcl
task "classify" {
  objective = "Classify the request type"
  router {
    route { target = tasks.handle_billing; condition = "Billing related" }
  }
}

task "handle_billing" {
  objective = "Determine the billing action"
  router {
    route { target = tasks.process_refund;   condition = "Customer wants a refund" }
    route { target = tasks.correct_invoice;  condition = "Invoice needs correction" }
  }
}

task "process_refund"   { objective = "Process the refund" }
task "correct_invoice"  { objective = "Fix the invoice" }
```

## Unconditional Routing (`send_to`)

`send_to` activates target tasks immediately when the source completes. No LLM decision — all targets fire.

```hcl
task "fetch" {
  objective = "Fetch data from the API"
  send_to   = [tasks.process_a, tasks.process_b]
}

task "process_a" { objective = "Process path A" }
task "process_b" { objective = "Process path B" }
```

This is useful for fan-out patterns where every branch should always run.

## Cross-Mission Routing

Route targets can reference other missions using `missions.name` instead of `tasks.name`. When a mission route is chosen, the current mission completes and the target mission launches as a new instance.

```hcl
task "handle_complaint" {
  objective = "Draft a response to the complaint"

  router {
    route {
      target    = missions.escalation_mission
      condition = "The complaint is severe and needs escalation"
    }
    route {
      target    = tasks.close_ticket
      condition = "The complaint can be resolved without escalation"
    }
  }
}
```

When the commander selects a mission route, it also provides any required inputs for the target mission. The `task_complete` tool presents the target mission's input requirements and the commander fills them in.

A mission can route to itself — this creates a new instance, not a loop. This is useful for retry or restart patterns.

### Full Example

```hcl
mission "escalation_mission" {
  directive = "Handle an escalated complaint"

  commander {
    model = models.anthropic.claude_haiku_4_5
  }
  agents = [agents.assistant]

  input "complaint_summary" {
    type        = "string"
    description = "Summary of the original complaint"
  }

  input "severity" {
    type        = "string"
    description = "Severity level"
    default     = "high"
  }

  task "triage" {
    objective = <<-EOT
      Perform high-priority triage for the escalated complaint.
      Complaint: "${inputs.complaint_summary}"
      Severity: "${inputs.severity}"
    EOT
  }
}

mission "support_triage" {
  directive = "Triage incoming customer messages"

  commander {
    model = models.anthropic.claude_haiku_4_5
  }
  agents = [agents.assistant]

  input "customer_message" {
    type        = "string"
    description = "The customer's message"
  }

  task "analyze" {
    objective = "Analyze the customer message: ${inputs.customer_message}"

    router {
      route {
        target    = tasks.handle_complaint
        condition = "The customer is unhappy"
      }
    }
  }

  task "handle_complaint" {
    objective = "Draft a response to the complaint"

    router {
      route {
        target    = missions.escalation_mission
        condition = "The complaint mentions legal action or cancellation"
      }
      route {
        target    = tasks.close_ticket
        condition = "The complaint can be resolved directly"
      }
    }
  }

  task "close_ticket" {
    objective = "Close the support ticket with a summary"
  }
}
```

## Static vs Dynamic Tasks

Tasks are either **statically scheduled** (part of the initial topological sort) or **dynamically activated** (only run when a `router` or `send_to` fires).

A dynamically activated task is the root of its own sub-DAG. These two worlds do not mix:

- **Dynamic targets cannot have `depends_on`.** They start when activated, not when some other set of tasks completes.
- **No task can `depends_on` a dynamic target.** If a dynamic target is never activated, the dependent task would hang forever.
- **`router` and `send_to` are mutually exclusive.** A task pushes work either conditionally or unconditionally, not both.

### First-One-Wins

A dynamic target can be referenced by multiple routers or `send_to` sources. The first activation wins — once a task starts, subsequent activations are silently ignored. Tasks run at most once.

### Ancestor Context

When a dynamically activated task starts, the runner treats the activating task as its parent for context purposes. The routed-to task gets access to the full ancestry of the task that activated it — the same depth of context as if it had `depends_on`.

## Validation Rules

| Rule | Description |
|------|-------------|
| No cycles | Combined `depends_on` + router + `send_to` edges must be acyclic |
| Dynamic targets cannot have `depends_on` | Tasks reachable via router or `send_to` cannot also declare dependencies |
| No task can `depends_on` a dynamic target | Static tasks cannot wait on dynamically activated tasks |
| `router` and `send_to` mutually exclusive | A task can have one or the other, not both |
| No self-routing / self-send | A task cannot target itself |
| No duplicate targets | Each target can appear at most once within a router or `send_to` |
| Targets must exist | Task targets must reference existing tasks; mission targets must reference existing missions |
| Parallel iterators cannot have a router | Sequential iterators can — the route is evaluated after the final iteration |
| At least one startable task | Every mission must have at least one task with no dependencies that isn't router-only |

## Iterator Interaction

- **Parallel iterators** cannot have a `router` (each iteration is independent — there's no single decision point)
- **Sequential iterators** can have a `router` — the route is evaluated after the final iteration completes
- Both parallel and sequential iterators can use `send_to` — targets activate after iteration completes

## Persistence & Resume

Route decisions are persisted to the database. On resume:

1. Route decisions are loaded to reconstruct which task activated which
2. Incomplete dynamic targets are re-queued for execution
3. The activating task's commander is restored so the dynamic target can query it for context

## See Also

- [Tasks](/squadron/missions/tasks) - Task configuration and dependencies
- [Internal Tools](/squadron/missions/internal-tools) - `task_complete` routing parameters
- [Missions Overview](/squadron/missions/overview) - Mission structure and execution

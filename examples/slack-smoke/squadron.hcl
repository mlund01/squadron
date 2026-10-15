variable "anthropic_api_key" {
  secret = true
}
variable "slack_bot_token" {
  secret = true
}
variable "slack_app_token" {
  secret = true
}
variable "slack_channel_id" {}
variable "mcp_host_secret" {
  secret = true
}

storage {
  backend = "sqlite"
}
model "anthropic" {
  provider = "anthropic"
  api_key  = vars.anthropic_api_key
}
mcp_host {
  enabled = true
  port    = 18090
  secret  = vars.mcp_host_secret
}
gateway "slack" {
  version = "local"
  settings = {
    bot_token       = vars.slack_bot_token
    app_token       = vars.slack_app_token
    channel_id      = vars.slack_channel_id
    checkpoint_path = ".squadron/slack-gateway.json"
    mcp_url         = "http://127.0.0.1:18090/mcp"
    mcp_token       = vars.mcp_host_secret
  }
}
agent "slack_tester" {
  personality = "Follow the synthetic Slack test steps exactly. Report tool errors and never invent successful posts."
  model = models.anthropic.claude_haiku_4_5
  tools = [builtins.gateway.post, builtins.human.ask]
}

mission "slack_smoke" {
  commander {
    model = models.anthropic.claude_haiku_4_5
  }
  agents = [agents.slack_tester]
  input "case_key" {
    type = "string"
    description = "Unique test request key, for example SLACK-TEST-001. Reuse it to test joining a thread."
  }
  input "states" {
    type = "list"
    items = { type = "string" }
    default = ["CO", "PA"]
    description = "Synthetic states. Enter JSON, for example [\"CO\", \"PA\"]."
  }
  input "fail_after_reply" {
    type = "bool"
    default = false
    description = "Use true to exercise a mission_failed notification instead of routing."
  }
  input "hidden_marker" {
    type = "string"
    protected = true
    value = "synthetic-not-a-secret"
  }
  notification {
    gateway {
      events = ["mission_failed"]
    }
  }
  task "exercise_messages" {
    objective = <<-EOT
      This is a Slack transport test using synthetic data. Delegate the following
      steps to slack_tester, in order, using case key ${inputs.case_key}:
      1. Call gateway.post with thread equal to the case key, root=true, and text
         "*<case key>*\nStatus: testing Slack messages".
      2. Post "Synthetic test reply" with the same thread key and root omitted.
      3. Ask "Which synthetic state should we inspect?" with choices CO and PA.
      4. Ask "Add a synthetic test note" without choices, so Slack uses an Answer modal.
      5. Post the two answers as a reply in the same thread, without mentioning anyone.
      Do not claim a post succeeded if the tool reports an error.
      After the agent finishes: if fail_after_reply=${inputs.fail_after_reply} is true,
      call task_complete with succeed=false and reason="Intentional Slack smoke-test failure".
      Otherwise, route to slack_smoke_finish, passing case_key=${inputs.case_key}.
    EOT
    router {
      route {
        target = missions.slack_smoke_finish
        condition = "The message and answer steps succeeded, and fail_after_reply is false."
      }
    }
  }
}

mission "slack_smoke_finish" {
  commander {
    model = models.anthropic.claude_haiku_4_5
  }
  agents = [agents.slack_tester]
  input "case_key" {
    type = "string"
    description = "The synthetic request key whose existing thread this run should join."
  }
  task "finish" {
    objective = <<-EOT
      Delegate to slack_tester. First call gateway.post with thread=${inputs.case_key},
      root=true, and text="*${inputs.case_key}*\nStatus: routed run joined".
      This must happen before asking a question, because it associates this new run
      with the existing request thread. Then ask "Did the routed run stay in the same thread?"
      with choices Yes and No. Finally rewrite the root using the same key and root=true:
      "*${inputs.case_key}*\nStatus: test complete\nRouted-thread check: <human answer>".
      Do not invent ticket, PR, or session links. Complete the task after the tool calls succeed.
    EOT
  }
}

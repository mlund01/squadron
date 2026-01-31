# Comprehensive test for multi-turn agent-supervisor interaction
# Tests all patterns: ASK_SUPE, SUPERVISOR_RESPONSE, NEW_TASK, session persistence

workflow "test_full_interaction" {
  supervisor_model = models.anthropic.claude_sonnet_4
  agents           = [agents.assistant]

  # Task 1: Agent asks question, supervisor answers
  task "ask_and_answer" {
    objective = <<-EOT
      Call the assistant agent and ask it to "Write a greeting for our users".

      The agent should ask you: what language should it use?
      When it asks, respond with: "Use Spanish."

      The agent should then provide a Spanish greeting.
    EOT

    output {
      field "greeting" {
        type        = "string"
        description = "The greeting the agent created"
        required    = true
      }
      field "language" {
        type        = "string"
        description = "The language used"
        required    = true
      }
    }
  }

  # Task 2: Continue with the same agent for a follow-up
  task "follow_up" {
    depends_on = [tasks.ask_and_answer]

    objective = <<-EOT
      Using the SAME assistant agent from the previous task (it still has context):

      1. First, give it a new task: "Now translate that greeting to French."
      2. The agent should remember the greeting it created and translate it.

      Use the "task" parameter to give the new task.
    EOT

    output {
      field "french_greeting" {
        type        = "string"
        description = "The French translation"
        required    = true
      }
    }
  }
}

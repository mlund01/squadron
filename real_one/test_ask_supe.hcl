# Test workflow for multi-turn agent-supervisor interaction
# Tests: ASK_SUPE, SUPERVISOR_RESPONSE, NEW_TASK patterns

workflow "test_ask_supe" {
  supervisor_model = models.anthropic.claude_sonnet_4
  agents           = [agents.assistant]

  # Task 1: Agent should ask for clarification about which format to use
  task "format_data" {
    objective = <<-EOT
      The user wants you to format some data. Call the assistant agent to format the data.
      Note: You have not been told what format to use (JSON, CSV, XML, etc).
      The agent should ask you for clarification. When it does, tell it to use JSON format.
    EOT

    output {
      field "formatted_data" {
        type        = "string"
        description = "The formatted data"
        required    = true
      }
      field "format_used" {
        type        = "string"
        description = "The format that was used"
        required    = true
      }
    }
  }
}

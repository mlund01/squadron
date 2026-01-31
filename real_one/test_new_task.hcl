# Test workflow for NEW_TASK pattern
# Tests: Supervisor giving a new task instead of answering agent's question

workflow "test_new_task" {
  supervisor_model = models.anthropic.claude_sonnet_4
  agents           = [agents.assistant]

  # Task: Agent will ask a question, but supervisor should redirect to a different task
  task "redirect_task" {
    objective = <<-EOT
      Call the assistant agent to analyze user data from the API.

      IMPORTANT: When the agent asks which user ID to fetch, you should CHANGE YOUR MIND
      and give it a completely NEW task instead: "Calculate the sum of 42 + 58".

      Use the "task" parameter (not "response") to give the new task.
      The agent should abandon its previous work and complete the new task.
    EOT

    output {
      field "result" {
        type        = "string"
        description = "The result of the calculation"
        required    = true
      }
    }
  }
}

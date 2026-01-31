# Test workflow for multiple tasks in same agent session
# Tests: Persistent agent sessions across multiple call_agent invocations

workflow "test_multi_task" {
  supervisor_model = models.anthropic.claude_sonnet_4
  agents           = [agents.assistant]

  # Task: Give agent multiple tasks in sequence, reusing the same session
  task "sequential_tasks" {
    objective = <<-EOT
      You will give the assistant agent THREE tasks in sequence.
      After each task completes, give the next task to the SAME agent.

      Task sequence:
      1. First task: "What is 10 + 20? Reply with just the number."
      2. Second task: "Now multiply your previous answer by 3. Reply with just the number."
      3. Third task: "What was your first answer and your second answer? Summarize both."

      The agent should be able to reference its previous work because the session persists.
      Use the "task" parameter for each new task.

      Report all three answers in your output.
    EOT

    output {
      field "first_answer" {
        type        = "string"
        description = "Answer to first task"
        required    = true
      }
      field "second_answer" {
        type        = "string"
        description = "Answer to second task"
        required    = true
      }
      field "third_answer" {
        type        = "string"
        description = "Answer to third task"
        required    = true
      }
    }
  }
}

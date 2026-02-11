mission "simple_test" {
  commander = models.anthropic.claude_sonnet_4
  agents    = [agents.assistant]

  task "list_files" {
    objective = "List the files in the current directory and report the count."

    output {
      field "count" {
        type        = "number"
        description = "Number of files found"
        required    = true
      }
    }
  }

  task "summarize" {
    objective  = "Summarize what was found in the previous task."
    depends_on = [tasks.list_files]

    output {
      field "summary" {
        type        = "string"
        description = "Brief summary of the findings"
        required    = true
      }
    }
  }
}

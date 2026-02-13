mission "simple_test" {
  commander = models.anthropic.claude_sonnet_4
  agents    = [agents.assistant]

  dataset "files" {
    description = "Files in the current directory"
    schema {
      field "name" {
        type     = "string"
        required = true
      }
    }
  }

  task "list_files" {
    objective = "List all files in the current directory and add each one to the 'files' dataset. Only include regular files, not directories."

    output {
      field "file_count" {
        type        = "number"
        description = "Total number of files found"
        required    = true
      }
    }
  }

  task "review_file" {
    objective  = "Read the name of file '${item.name}' and make up what you think it might contain"
    depends_on = [tasks.list_files]

    iterator {
      dataset           = datasets.files
      parallel          = true
      concurrency_limit = 3
    }

    output {
      field "summary" {
        type        = "string"
        description = "A short summary of the file contents"
        required    = true
      }
    }
  }
}

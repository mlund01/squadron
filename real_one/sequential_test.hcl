workflow "sequential_test" {
  supervisor_model = models.openai.gpt_4o
  agents           = [agents.assistant]

  dataset "numbers" {
    description = "Simple numbers to process sequentially"
    schema {
      field "value" {
        type     = "number"
        required = true
      }
      field "label" {
        type = "string"
      }
    }
  }

  task "populate_dataset" {
    objective = <<-EOT
      Populate the 'numbers' dataset with exactly 3 items:
      1. value: 10, label: "first"
      2. value: 20, label: "second"
      3. value: 30, label: "third"

      Use the set_dataset tool to add these items.
    EOT
  }

  task "process_numbers" {
    objective = <<-EOT
      Process the number ${item.value} (${item.label}).

      Calculate and report:
      - The value doubled
      - The value squared
      - Whether the value is greater than 15

      This tests sequential iteration with a single agent session.
    EOT

    iterator {
      dataset     = datasets.numbers
      parallel    = false
      max_retries = 1
    }
    depends_on = [tasks.populate_dataset]

    output {
      field "original" {
        type        = "number"
        description = "The original value"
        required    = true
      }
      field "doubled" {
        type        = "number"
        description = "The value multiplied by 2"
        required    = true
      }
      field "squared" {
        type        = "number"
        description = "The value squared"
        required    = true
      }
      field "greater_than_15" {
        type        = "boolean"
        description = "Whether the value is greater than 15"
        required    = true
      }
    }
  }

  task "summarize" {
    objective  = "Summarize the results from processing all numbers. Report the sum of all doubled values and count how many values were greater than 15."
    depends_on = [tasks.process_numbers]

    output {
      field "total_doubled" {
        type        = "number"
        description = "Sum of all doubled values"
        required    = true
      }
      field "count_greater_than_15" {
        type        = "number"
        description = "How many values were greater than 15"
        required    = true
      }
    }
  }
}

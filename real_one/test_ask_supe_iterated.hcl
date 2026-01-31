# Test workflow for ask_supe with iterated tasks
# Tests: Querying specific iteration supervisors using the index parameter

workflow "test_ask_supe_iterated" {
  supervisor_model = models.anthropic.claude_sonnet_4
  agents           = [agents.assistant]

  # Dataset of fictional characters to create backstories for
  dataset "characters" {
    description = "Characters needing backstories"
    items = [
      { name = "Zara", role = "space pirate" },
      { name = "Marcus", role = "time-traveling chef" },
      { name = "Luna", role = "ghost detective" }
    ]
  }

  # Task 1: Iterated task - invent a backstory for each character
  task "create_backstories" {
    objective = <<-EOT
      Invent a creative backstory for the character: ${item.name}, who is a ${item.role}.

      Make up:
      - Their origin story (2-3 sentences)
      - Their greatest fear
      - Their secret talent

      Be creative and have fun with it!
    EOT

    iterator {
      dataset = "characters"
    }

    output {
      field "character_name" {
        type        = "string"
        description = "The character's name"
        required    = true
      }
      field "origin" {
        type        = "string"
        description = "Their origin story"
        required    = true
      }
      field "greatest_fear" {
        type        = "string"
        description = "What they fear most"
        required    = true
      }
      field "secret_talent" {
        type        = "string"
        description = "Their hidden ability"
        required    = true
      }
    }
  }

  # Task 2: Query specific iteration and ask follow-up questions
  task "character_crossover" {
    depends_on = ["create_backstories"]

    objective = <<-EOT
      Create a crossover story between two characters from the backstories.

      Steps:
      1. Use query_task_output to get all results from "create_backstories"
      2. Pick the TWO most interesting characters based on their backstories
      3. For EACH character you picked, use ask_supe with:
         - task_name: "create_backstories"
         - index: the iteration index from the query results
         - question: "How would this character react if they met someone from a completely different world?"
      4. Use both answers to write a short crossover scene (3-4 sentences)

      IMPORTANT: You MUST include the "index" parameter when calling ask_supe!

      Report which characters you chose and the crossover scene.
    EOT

    output {
      field "character_1" {
        type        = "string"
        description = "First character chosen"
        required    = true
      }
      field "character_2" {
        type        = "string"
        description = "Second character chosen"
        required    = true
      }
      field "crossover_scene" {
        type        = "string"
        description = "The crossover story"
        required    = true
      }
    }
  }
}

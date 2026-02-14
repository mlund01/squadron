mission "simple_test" {
  commander = models.anthropic.claude_sonnet_4
  agents    = [agents.assistant]

  dataset "words" {
    description = "Words to write stories about"
    schema {
      field "word" {
        type     = "string"
        required = true
      }
    }
  }

  task "generate_words" {
    objective = "Pick 3 random, interesting words and add each one to the 'words' dataset."

    output {
      field "word_count" {
        type        = "number"
        description = "Total number of words generated"
        required    = true
      }
    }
  }

  task "write_story" {
    objective  = "Write a story of 25 words or less inspired by the word '${item.word}'. Save the story to a file called 'stories/${item.word}.txt'."
    depends_on = [tasks.generate_words]

    iterator {
      dataset           = datasets.words
      parallel          = true
      concurrency_limit = 3
    }

    output {
      field "filename" {
        type        = "string"
        description = "The filename where the story was saved"
        required    = true
      }
    }
  }
}

# Copy this file into an otherwise empty configuration directory and run
# `squadron verify <directory>`. Set up the Anthropic connection in the
# Squadron product before running these example missions.
model_provider "anthropic" {
  models = ["claude_haiku_4_5"]
}

mission "linear_example" {
  commander { model = models.anthropic.claude_haiku_4_5 }
  models = [models.anthropic.claude_haiku_4_5]

  task "research" {
    objective = "Find three useful facts about the topic."
  }
  task "report" {
    objective  = "Summarize the research in three bullets."
    depends_on = [tasks.research]
  }
}

mission "parallel_example" {
  commander { model = models.anthropic.claude_haiku_4_5 }
  models       = [models.anthropic.claude_haiku_4_5]
  max_parallel = 2

  task "audience" { objective = "Describe the target audience." }
  task "benefit"  { objective = "Describe one concrete benefit." }
  task "combine" {
    objective  = "Combine the audience and benefit into a launch line."
    depends_on = [tasks.audience, tasks.benefit]
  }
}

mission "routed_example" {
  commander { model = models.anthropic.claude_haiku_4_5 }
  models = [models.anthropic.claude_haiku_4_5]

  task "classify" {
    objective = "Classify the request as billing or support. Include evidence in the result."
    router {
      route {
        target    = tasks.billing
        condition = "The request concerns a charge or invoice."
      }
      route {
        target    = tasks.support
        condition = "The request concerns product behavior."
      }
    }
  }
  task "billing" { objective = "Draft a billing response." }
  task "support" { objective = "Draft a support response." }
}

mission "iterative_example" {
  commander { model = models.anthropic.claude_haiku_4_5 }
  models = [models.anthropic.claude_haiku_4_5]

  dataset "audiences" {
    description = "Three audiences for a launch message"
    items = [
      { name = "renters" },
      { name = "parents" },
      { name = "gardeners" },
    ]
  }
  task "benefit" {
    objective = "Write one benefit for this audience item."
    iterator {
      dataset           = datasets.audiences
      parallel          = true
      concurrency_limit = 2
    }
  }
  task "summary" {
    objective  = "Summarize all accepted audience benefits."
    depends_on = [tasks.benefit]
  }
}

mission "retry_example" {
  commander { model = models.anthropic.claude_haiku_4_5 }
  models = [models.anthropic.claude_haiku_4_5]

  task "fetch" {
    objective           = "Fetch the source and report what it says."
    max_retries         = 2
    retry_delay_seconds = 10
    timeout_seconds     = 60
  }
}

mission "human_example" {
  commander { model = models.anthropic.claude_haiku_4_5 }
  models = [models.anthropic.claude_haiku_4_5]

  task "approve" {
    objective = "Ask the operator to approve the proposed note, then report the answer."
    tools     = [builtins.human.ask]
  }
}

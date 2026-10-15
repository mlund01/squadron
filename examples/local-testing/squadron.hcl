storage {
  backend = "sqlite"
}
model "local_test" {
  provider = "anthropic"
  api_key = "local-test-placeholder"
  base_url = "__MODEL_URL__"
}
agent "worker" {
  model = models.local_test.claude_haiku_4_5
  personality = "Work only with the synthetic local test data."
}
mission "local_routing" {
  agents = [agents.worker]
  commander {
    model = models.local_test.claude_haiku_4_5
  }
  input "case_key" {
    type = "string"
  }
  task "classify" {
    objective = "LOCAL_CLASSIFY: classify synthetic case ${inputs.case_key}."
    router {
      route {
        target = tasks.repair
        condition = "The synthetic case requires repair."
      }
      route {
        target = tasks.dismiss
        condition = "The synthetic case needs no change."
      }
    }
  }
  task "repair" {
    objective = "LOCAL_REPAIR: record a synthetic repair for ${inputs.case_key}."
  }
  task "dismiss" {
    objective = "LOCAL_DISMISS: record that no repair is needed."
  }
}
mission "local_failure" {
  agents = [agents.worker]
  commander {
    model = models.local_test.claude_haiku_4_5
  }
  task "fail" {
    objective = "LOCAL_FAILURE: exercise failure persistence with a synthetic error."
  }
}

mission "local_delegation" {
  commander {
    model = models.local_test.claude_haiku_4_5
  }
  agents = [agents.worker]
  task "delegate" {
    objective = "LOCAL_DELEGATE: ask the worker to count the synthetic records."
  }
}

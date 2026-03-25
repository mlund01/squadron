package config_test

import (
	"squadron/config"

	"github.com/zclconf/go-cty/cty"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Mission", func() {

	Describe("parsing", func() {
		It("parses a minimal mission with one task", func() {
			hcl := fullBaseHCL() + `
mission "simple" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  task "only_task" {
    objective = "Do something"
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions).To(HaveLen(1))
			Expect(cfg.Missions[0].Name).To(Equal("simple"))
			Expect(cfg.Missions[0].Commander.Model).To(Equal("claude_sonnet_4"))
			Expect(cfg.Missions[0].Agents).To(ConsistOf("test_agent"))
			Expect(cfg.Missions[0].Tasks).To(HaveLen(1))
			Expect(cfg.Missions[0].Tasks[0].Name).To(Equal("only_task"))
		})

		It("parses mission with task dependencies", func() {
			hcl := fullBaseHCL() + `
mission "chained" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  task "first" { objective = "Step 1" }
  task "second" {
    objective  = "Step 2"
    depends_on = [tasks.first]
  }
  task "third" {
    objective  = "Step 3"
    depends_on = [tasks.first, tasks.second]
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Tasks[1].DependsOn).To(ConsistOf("first"))
			Expect(cfg.Missions[0].Tasks[2].DependsOn).To(ConsistOf("first", "second"))
		})

		It("parses mission with inputs", func() {
			hcl := fullBaseHCL() + `
mission "with_inputs" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  input "target_url" {
    type        = "string"
    description = "URL to process"
    default     = "https://example.com"
  }
  input "count" {
    type    = "number"
    default = 5
  }
  task "work" {
    objective = "Process the inputs"
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Inputs).To(HaveLen(2))
			Expect(cfg.Missions[0].Inputs[0].Name).To(Equal("target_url"))
			Expect(cfg.Missions[0].Inputs[0].Type).To(Equal("string"))
			Expect(cfg.Missions[0].Inputs[1].Name).To(Equal("count"))
			Expect(cfg.Missions[0].Inputs[1].Type).To(Equal("number"))
		})

		It("parses mission with secret input", func() {
			hcl := fullBaseHCL() + `
mission "with_secret" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  input "api_token" {
    type   = "string"
    secret = true
    value  = vars.test_api_key
  }
  task "work" { objective = "Do work" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Inputs[0].Secret).To(BeTrue())
			Expect(cfg.Missions[0].Inputs[0].Value).NotTo(BeNil())
		})

		It("parses mission with dataset and schema", func() {
			hcl := fullBaseHCL() + `
mission "with_dataset" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  dataset "cities" {
    description = "City list"
    schema {
      field "name" {
        type     = "string"
        required = true
      }
      field "pop" {
        type = "number"
      }
    }
  }
  task "process" {
    objective = "Process cities"
    iterator {
      dataset = datasets.cities
    }
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Datasets).To(HaveLen(1))
			Expect(cfg.Missions[0].Datasets[0].Name).To(Equal("cities"))
			Expect(cfg.Missions[0].Datasets[0].Description).To(Equal("City list"))
			Expect(cfg.Missions[0].Datasets[0].Schema).NotTo(BeNil())
			Expect(cfg.Missions[0].Datasets[0].Schema.Fields).To(HaveLen(2))
		})

		It("parses mission with task output schema", func() {
			hcl := fullBaseHCL() + `
mission "with_output" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  task "analyze" {
    objective = "Analyze data"
    output {
      field "result" {
        type     = "string"
        required = true
      }
      field "score" {
        type = "number"
      }
    }
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Tasks[0].Output).NotTo(BeNil())
			Expect(cfg.Missions[0].Tasks[0].Output.Fields).To(HaveLen(2))
			Expect(cfg.Missions[0].Tasks[0].Output.Fields[0].Name).To(Equal("result"))
			Expect(cfg.Missions[0].Tasks[0].Output.Fields[0].Required).To(BeTrue())
		})

		It("parses mission with parallel iterator options", func() {
			hcl := fullBaseHCL() + `
mission "parallel" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  dataset "items" { description = "Items" }
  task "process" {
    objective = "Process items"
    iterator {
      dataset           = datasets.items
      parallel          = true
      concurrency_limit = 10
      start_delay       = 500
      smoketest         = true
      max_retries       = 3
    }
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			iter := cfg.Missions[0].Tasks[0].Iterator
			Expect(iter).NotTo(BeNil())
			Expect(iter.Parallel).To(BeTrue())
			Expect(iter.ConcurrencyLimit).To(Equal(10))
			Expect(iter.StartDelay).To(Equal(500))
			Expect(iter.Smoketest).To(BeTrue())
			Expect(iter.MaxRetries).To(Equal(3))
		})

		It("parses dataset with bind_to input reference", func() {
			hcl := fullBaseHCL() + `
mission "bound" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  input "urls" {
    type = "list"
  }
  dataset "url_list" {
    bind_to = inputs.urls
  }
  task "fetch" {
    objective = "Fetch urls"
    iterator { dataset = datasets.url_list }
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Datasets[0].BindTo).To(Equal("urls"))
		})

		It("parses mission with task-level agents", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
agent "agent_a" {
  model       = models.anthropic.claude_sonnet_4
  personality = "A"
  role        = "Agent A"
  tools       = [builtins.bash.bash]
}
agent "agent_b" {
  model       = models.anthropic.claude_sonnet_4
  personality = "B"
  role        = "Agent B"
  tools       = [builtins.http.get]
}
mission "multi_agent" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.agent_a]
  task "task1" { objective = "First task" }
  task "task2" {
    objective = "Second task"
    agents    = [agents.agent_b]
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Missions[0].Tasks[0].Agents).To(BeEmpty())
			Expect(cfg.Missions[0].Tasks[1].Agents).To(ConsistOf("agent_b"))
		})
	})

	Describe("Validate", func() {
		Context("mission-level", func() {
			It("rejects mission with no commander", func() {
				hcl := fullBaseHCL() + `
mission "no_commander" {
  agents    = [agents.test_agent]
  task "t" { objective = "Do work" }
}
`
				_, f := writeFixture("config.hcl", hcl)
				_, err := config.LoadFile(f)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("commander block is required"))
			})

			It("rejects mission with zero tasks", func() {
				m := config.Mission{Name: "empty", Commander: &config.MissionCommander{Model: "claude_sonnet_4"}, Agents: []string{"a"}}
				models := []config.Model{{Provider: "anthropic", AllowedModels: []string{"claude_sonnet_4"}}}
				agents := []config.Agent{{Name: "a"}}
				err := m.Validate(models, agents, nil, nil)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("at least one task"))
			})

			It("rejects mission with unknown agent reference", func() {
				hcl := fullBaseHCL() + `
mission "bad_agent" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  task "t" { objective = "Do work" }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				// Manually modify to add a bad agent ref for validation
				cfg.Missions[0].Agents = append(cfg.Missions[0].Agents, "ghost_agent")
				err = cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ghost_agent"))
				Expect(err.Error()).To(ContainSubstring("not found"))
			})

			It("rejects duplicate task names", func() {
				hcl := fullBaseHCL() + `
mission "dup_tasks" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  task "same_name" { objective = "First" }
  task "same_name" { objective = "Second" }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				err = cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("duplicate task name"))
			})

			It("accepts valid mission with multiple tasks", func() {
				hcl := fullBaseHCL() + `
mission "valid" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  task "a" { objective = "Task A" }
  task "b" {
    objective  = "Task B"
    depends_on = [tasks.a]
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				_, err := config.LoadAndValidate(f)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("MissionInput validation", func() {
			DescribeTable("accepts valid input types",
				func(inputType string) {
					input := config.MissionInput{Name: "test", Type: inputType}
					Expect(input.Validate()).To(Succeed())
				},
				Entry("string", "string"),
				Entry("number", "number"),
				Entry("bool", "bool"),
				Entry("list", "list"),
				Entry("object", "object"),
			)

			It("rejects invalid input type", func() {
				input := config.MissionInput{Name: "test", Type: "float"}
				err := input.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("invalid type"))
			})

			It("rejects secret input without value", func() {
				input := config.MissionInput{Name: "test", Type: "string", Secret: true, Value: nil}
				err := input.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("secret input must have a value"))
			})

			It("rejects secret input with non-string value", func() {
				boolVal := cty.BoolVal(true)
				input := config.MissionInput{Name: "test", Type: "string", Secret: true, Value: &boolVal}
				err := input.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("secret value must be a string"))
			})

			It("accepts secret input with string value", func() {
				strVal := cty.StringVal("my-secret")
				input := config.MissionInput{Name: "test", Type: "string", Secret: true, Value: &strVal}
				Expect(input.Validate()).To(Succeed())
			})
		})

		Context("Dataset validation", func() {
			It("rejects dataset with empty name", func() {
				d := config.Dataset{Name: ""}
				err := d.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("name is required"))
			})
		})

		Context("Task validation", func() {
			It("rejects task self-dependency", func() {
				hcl := fullBaseHCL() + `
mission "self_dep" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  task "loop" {
    objective  = "I depend on myself"
    depends_on = [tasks.loop]
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				err = cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("depend on itself"))
			})
		})

		Context("TaskIterator validation", func() {
			It("rejects iterator with empty dataset", func() {
				ti := config.TaskIterator{Dataset: ""}
				err := ti.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("dataset is required"))
			})

			It("rejects concurrency_limit when parallel=false", func() {
				hcl := fullBaseHCL() + `
mission "bad_iter" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  dataset "items" { description = "Items" }
  task "work" {
    objective = "Do work"
    iterator {
      dataset           = datasets.items
      parallel          = false
      concurrency_limit = 5
    }
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				_, err := config.LoadFile(f)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("concurrency_limit is only valid when parallel=true"))
			})

			It("rejects start_delay when parallel=false", func() {
				hcl := fullBaseHCL() + `
mission "bad_iter2" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  dataset "items" { description = "Items" }
  task "work" {
    objective = "Do work"
    iterator {
      dataset     = datasets.items
      parallel    = false
      start_delay = 100
    }
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				_, err := config.LoadFile(f)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("start_delay is only valid when parallel=true"))
			})

			It("rejects smoketest when parallel=false", func() {
				hcl := fullBaseHCL() + `
mission "bad_iter3" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  dataset "items" { description = "Items" }
  task "work" {
    objective = "Do work"
    iterator {
      dataset   = datasets.items
      parallel  = false
      smoketest = true
    }
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				_, err := config.LoadFile(f)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("smoketest is only valid when parallel=true"))
			})

			It("accepts parallel-specific options when parallel=true", func() {
				hcl := fullBaseHCL() + `
mission "good_iter" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  dataset "items" { description = "Items" }
  task "work" {
    objective = "Do work"
    iterator {
      dataset           = datasets.items
      parallel          = true
      concurrency_limit = 10
      start_delay       = 200
      smoketest         = true
    }
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.Missions[0].Tasks[0].Iterator.Parallel).To(BeTrue())
			})
		})

		Context("DAG cycle detection", func() {
			It("accepts linear dependency chain", func() {
				hcl := fullBaseHCL() + `
mission "linear" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  task "a" {
    objective = "A"
  }
  task "b" {
    objective  = "B"
    depends_on = [tasks.a]
  }
  task "c" {
    objective  = "C"
    depends_on = [tasks.b]
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				_, err := config.LoadAndValidate(f)
				Expect(err).NotTo(HaveOccurred())
			})

			It("detects direct cycle A -> B -> A", func() {
				hcl := fullBaseHCL() + `
mission "cycle" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  task "a" {
    objective  = "Task A"
    depends_on = [tasks.b]
  }
  task "b" {
    objective  = "Task B"
    depends_on = [tasks.a]
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				err = cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("cycle"))
			})

			It("detects indirect cycle A -> B -> C -> A", func() {
				hcl := fullBaseHCL() + `
mission "long_cycle" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  task "a" {
    objective  = "Task A"
    depends_on = [tasks.c]
  }
  task "b" {
    objective  = "Task B"
    depends_on = [tasks.a]
  }
  task "c" {
    objective  = "Task C"
    depends_on = [tasks.b]
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				err = cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("cycle"))
			})

			It("accepts diamond dependency (no cycle)", func() {
				hcl := fullBaseHCL() + `
mission "diamond" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents    = [agents.test_agent]
  task "root" { objective = "Root" }
  task "left" {
    objective  = "Left"
    depends_on = [tasks.root]
  }
  task "right" {
    objective  = "Right"
    depends_on = [tasks.root]
  }
  task "final" {
    objective  = "Final"
    depends_on = [tasks.left, tasks.right]
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				_, err := config.LoadAndValidate(f)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("Router validation", func() {
			It("accepts valid router with routes", func() {
				hcl := fullBaseHCL() + `
mission "valid_router" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]
  task "classify" {
    objective = "Classify input"
    router {
      route {
        target    = tasks.handle_a
        condition = "Input is type A"
      }
      route {
        target    = tasks.handle_b
        condition = "Input is type B"
      }
    }
  }
  task "handle_a" { objective = "Handle A" }
  task "handle_b" { objective = "Handle B" }
}
`
				_, f := writeFixture("config.hcl", hcl)
				_, err := config.LoadAndValidate(f)
				Expect(err).NotTo(HaveOccurred())
			})

			It("rejects router target with depends_on", func() {
				hcl := fullBaseHCL() + `
mission "bad_router" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]
  task "start" { objective = "Start" }
  task "classify" {
    objective  = "Classify"
    depends_on = [tasks.start]
    router {
      route {
        target    = tasks.handle_a
        condition = "Type A"
      }
    }
  }
  task "handle_a" {
    objective  = "Handle A"
    depends_on = [tasks.start]
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				err = cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("dynamically activated tasks cannot have depends_on"))
			})

			It("rejects task depending on a router target", func() {
				hcl := fullBaseHCL() + `
mission "dep_on_dynamic" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]
  task "classify" {
    objective = "Classify"
    router {
      route {
        target    = tasks.handle_a
        condition = "Type A"
      }
    }
  }
  task "handle_a" { objective = "Handle A" }
  task "after" {
    objective  = "Runs after handle_a"
    depends_on = [tasks.handle_a]
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				err = cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("cannot depend on 'handle_a'"))
				Expect(err.Error()).To(ContainSubstring("dynamically activated"))
			})

			It("rejects router that creates a cycle", func() {
				hcl := fullBaseHCL() + `
mission "router_cycle" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]
  task "a" {
    objective = "A"
    router {
      route {
        target    = tasks.b
        condition = "Go to B"
      }
    }
  }
  task "b" {
    objective = "B"
    router {
      route {
        target    = tasks.a
        condition = "Go back to A"
      }
    }
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				err = cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("cycle"))
			})

			It("rejects router with self-route", func() {
				hcl := fullBaseHCL() + `
mission "self_route" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]
  task "loop" {
    objective = "Loop"
    router {
      route {
        target    = tasks.loop
        condition = "Route to self"
      }
    }
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				err = cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("route to itself"))
			})

			It("rejects router on parallel iterator", func() {
				hcl := fullBaseHCL() + `
mission "par_router" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]
  dataset "items" { description = "Items" }
  task "work" {
    objective = "Work"
    iterator {
      dataset  = datasets.items
      parallel = true
    }
    router {
      route {
        target    = tasks.next
        condition = "Continue"
      }
    }
  }
  task "next" { objective = "Next" }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				err = cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("parallel iterators cannot have a router"))
			})

			It("allows multiple routers targeting the same task", func() {
				hcl := fullBaseHCL() + `
mission "multi_router" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]
  task "classify_a" {
    objective = "Classify A"
    router {
      route {
        target    = tasks.shared_handler
        condition = "Needs shared handling"
      }
    }
  }
  task "classify_b" {
    objective = "Classify B"
    router {
      route {
        target    = tasks.shared_handler
        condition = "Also needs shared handling"
      }
    }
  }
  task "shared_handler" { objective = "Handle both" }
}
`
				_, f := writeFixture("config.hcl", hcl)
				_, err := config.LoadAndValidate(f)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("send_to validation", func() {
			It("accepts valid send_to", func() {
				hcl := fullBaseHCL() + `
mission "valid_sendto" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]
  task "start" {
    objective = "Start"
    send_to   = [tasks.next_a, tasks.next_b]
  }
  task "next_a" { objective = "A" }
  task "next_b" { objective = "B" }
}
`
				_, f := writeFixture("config.hcl", hcl)
				_, err := config.LoadAndValidate(f)
				Expect(err).NotTo(HaveOccurred())
			})

			It("rejects send_to with self-reference", func() {
				hcl := fullBaseHCL() + `
mission "self_send" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]
  task "loop" {
    objective = "Loop"
    send_to   = [tasks.loop]
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				err = cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("send to itself"))
			})

			It("rejects send_to combined with router", func() {
				hcl := fullBaseHCL() + `
mission "both" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]
  task "start" {
    objective = "Start"
    send_to   = [tasks.next]
    router {
      route {
        target    = tasks.other
        condition = "Go other"
      }
    }
  }
  task "next"  { objective = "Next" }
  task "other" { objective = "Other" }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				err = cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("both send_to and router"))
			})

			It("rejects send_to target with depends_on", func() {
				hcl := fullBaseHCL() + `
mission "sendto_dep" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]
  task "first"  { objective = "First" }
  task "sender" {
    objective  = "Sender"
    depends_on = [tasks.first]
    send_to    = [tasks.target]
  }
  task "target" {
    objective  = "Target"
    depends_on = [tasks.first]
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				err = cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("dynamically activated tasks cannot have depends_on"))
			})

			It("rejects task depending on a send_to target", func() {
				hcl := fullBaseHCL() + `
mission "dep_on_sendto" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]
  task "sender" {
    objective = "Send"
    send_to   = [tasks.dynamic]
  }
  task "dynamic" { objective = "Dynamic" }
  task "bad" {
    objective  = "Depends on dynamic"
    depends_on = [tasks.dynamic]
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				err = cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("cannot depend on 'dynamic'"))
				Expect(err.Error()).To(ContainSubstring("dynamically activated"))
			})

			It("rejects send_to that creates a cycle", func() {
				hcl := fullBaseHCL() + `
mission "sendto_cycle" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]
  task "a" {
    objective = "A"
    send_to   = [tasks.b]
  }
  task "b" {
    objective = "B"
    send_to   = [tasks.a]
  }
}
`
				_, f := writeFixture("config.hcl", hcl)
				cfg, err := config.LoadFile(f)
				Expect(err).NotTo(HaveOccurred())
				err = cfg.Validate()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("cycle"))
			})
		})
	})

	Describe("GetRootTasks", func() {
		It("returns only tasks with no dependencies", func() {
			m := config.Mission{
				Tasks: []config.Task{
					{Name: "root1"},
					{Name: "child", DependsOn: []string{"root1"}},
					{Name: "root2"},
				},
			}
			roots := m.GetRootTasks()
			Expect(roots).To(HaveLen(2))
			names := []string{roots[0].Name, roots[1].Name}
			Expect(names).To(ConsistOf("root1", "root2"))
		})
	})
})

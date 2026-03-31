package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Schema helper functions", func() {

	// ─────────────────────────────────────────────────────────────────────────
	// Tool inputs shorthand
	// ─────────────────────────────────────────────────────────────────────────

	Describe("tool inputs = { ... } shorthand", func() {

		It("parses primitive field definitions", func() {
			hclSrc := minimalVarsHCL() + minimalModelHCL() + `
tool "weather" {
  implements = builtins.http.get
  inputs = {
    city  = string("The target city", true)
    units = string("Temperature units")
    limit = number("Max results")
  }
  url = "https://wttr.in/${inputs.city}"
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.CustomTools).To(HaveLen(1))

			tool := cfg.CustomTools[0]
			Expect(tool.Inputs).NotTo(BeNil())
			Expect(tool.Inputs.Fields).To(HaveLen(3))

			city := findInputField(tool.Inputs.Fields, "city")
			Expect(city).NotTo(BeNil())
			Expect(city.Type).To(Equal("string"))
			Expect(city.Description).To(Equal("The target city"))
			Expect(city.Required).To(BeTrue())

			units := findInputField(tool.Inputs.Fields, "units")
			Expect(units).NotTo(BeNil())
			Expect(units.Type).To(Equal("string"))
			Expect(units.Required).To(BeFalse())

			limit := findInputField(tool.Inputs.Fields, "limit")
			Expect(limit).NotTo(BeNil())
			Expect(limit.Type).To(Equal("number"))
		})

		It("parses all primitive types", func() {
			hclSrc := minimalVarsHCL() + minimalModelHCL() + `
tool "mixed" {
  implements = builtins.http.get
  inputs = {
    name    = string("A string field", true)
    score   = number("A number field")
    count   = integer("An integer field", true)
    active  = bool("A boolean field")
  }
  url = "https://example.com"
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			fields := cfg.CustomTools[0].Inputs.Fields

			Expect(findInputField(fields, "name").Type).To(Equal("string"))
			Expect(findInputField(fields, "score").Type).To(Equal("number"))
			Expect(findInputField(fields, "count").Type).To(Equal("integer"))
			Expect(findInputField(fields, "active").Type).To(Equal("boolean"))
		})

		It("parses a list(string, ...) field", func() {
			hclSrc := minimalVarsHCL() + minimalModelHCL() + `
tool "search" {
  implements = builtins.http.get
  inputs = {
    tags = list(string, "Tags to filter by", true)
  }
  url = "https://example.com"
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			fields := cfg.CustomTools[0].Inputs.Fields

			tags := findInputField(fields, "tags")
			Expect(tags).NotTo(BeNil())
			Expect(tags.Type).To(Equal("array"))
			Expect(tags.Description).To(Equal("Tags to filter by"))
			Expect(tags.Required).To(BeTrue())
			Expect(tags.Items).NotTo(BeNil())
			Expect(tags.Items.Type).To(Equal("string"))
		})

		It("parses a list(number, ...) field", func() {
			hclSrc := minimalVarsHCL() + minimalModelHCL() + `
tool "stats" {
  implements = builtins.http.get
  inputs = {
    scores = list(number, "Score list")
  }
  url = "https://example.com"
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			scores := findInputField(cfg.CustomTools[0].Inputs.Fields, "scores")
			Expect(scores.Type).To(Equal("array"))
			Expect(scores.Items.Type).To(Equal("number"))
		})

		It("parses a nested object field", func() {
			hclSrc := minimalVarsHCL() + minimalModelHCL() + `
tool "geo" {
  implements = builtins.http.get
  inputs = {
    coords = object({
      lat = number("Latitude", true)
      lon = number("Longitude", true)
    }, "Geographic coordinates", true)
  }
  url = "https://example.com"
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			fields := cfg.CustomTools[0].Inputs.Fields

			coords := findInputField(fields, "coords")
			Expect(coords).NotTo(BeNil())
			Expect(coords.Type).To(Equal("object"))
			Expect(coords.Description).To(Equal("Geographic coordinates"))
			Expect(coords.Required).To(BeTrue())
			Expect(coords.Properties).To(HaveLen(2))

			lat := findInputField(coords.Properties, "lat")
			Expect(lat).NotTo(BeNil())
			Expect(lat.Type).To(Equal("number"))
			Expect(lat.Required).To(BeTrue())

			lon := findInputField(coords.Properties, "lon")
			Expect(lon).NotTo(BeNil())
			Expect(lon.Type).To(Equal("number"))
		})

		It("parses a list of objects (nested composite)", func() {
			hclSrc := minimalVarsHCL() + minimalModelHCL() + `
tool "order" {
  implements = builtins.http.post
  inputs = {
    items = list(object({
      name = string("Item name", true)
      qty  = number("Quantity")
    }), "Order items", true)
  }
  url  = "https://example.com/orders"
  body = { items = inputs.items }
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			fields := cfg.CustomTools[0].Inputs.Fields

			items := findInputField(fields, "items")
			Expect(items).NotTo(BeNil())
			Expect(items.Type).To(Equal("array"))
			Expect(items.Required).To(BeTrue())
			Expect(items.Items).NotTo(BeNil())
			Expect(items.Items.Type).To(Equal("object"))
			Expect(items.Items.Properties).To(HaveLen(2))

			name := findInputField(items.Items.Properties, "name")
			Expect(name.Type).To(Equal("string"))
			Expect(name.Required).To(BeTrue())

			qty := findInputField(items.Items.Properties, "qty")
			Expect(qty.Type).To(Equal("number"))
			Expect(qty.Required).To(BeFalse())
		})

		It("parses a map field", func() {
			hclSrc := minimalVarsHCL() + minimalModelHCL() + `
tool "meta" {
  implements = builtins.http.post
  inputs = {
    metadata = map(string, "Arbitrary key-value pairs")
  }
  url  = "https://example.com"
  body = { meta = inputs.metadata }
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			meta := findInputField(cfg.CustomTools[0].Inputs.Fields, "metadata")
			Expect(meta).NotTo(BeNil())
			Expect(meta.Type).To(Equal("map"))
			Expect(meta.Items).NotTo(BeNil())
			Expect(meta.Items.Type).To(Equal("string"))
		})

		It("still parses the verbose inputs block form", func() {
			hclSrc := minimalVarsHCL() + minimalModelHCL() + `
tool "legacy" {
  implements = builtins.http.get
  inputs {
    field "city" {
      type        = "string"
      description = "Target city"
      required    = true
    }
  }
  url = "https://example.com"
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.CustomTools[0].Inputs.Fields).To(HaveLen(1))
			Expect(cfg.CustomTools[0].Inputs.Fields[0].Name).To(Equal("city"))
		})

		It("converts shorthand inputs to aitools schema with Required list", func() {
			hclSrc := minimalVarsHCL() + minimalModelHCL() + `
tool "req_test" {
  implements = builtins.http.get
  inputs = {
    a = string("First", true)
    b = string("Second")
    c = number("Third", true)
  }
  url = "https://example.com"
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			schema := cfg.CustomTools[0].Inputs.ToAIToolsSchema()
			Expect(schema.Required).To(ConsistOf("a", "c"))
			Expect(schema.Properties).To(HaveKey("a"))
			Expect(schema.Properties).To(HaveKey("b"))
			Expect(schema.Properties).To(HaveKey("c"))
		})
	})

	// ─────────────────────────────────────────────────────────────────────────
	// Task output shorthand
	// ─────────────────────────────────────────────────────────────────────────

	Describe("task output = { ... } shorthand", func() {

		It("parses task output as object shorthand", func() {
			hclSrc := fullBaseHCL() + `
mission "research" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]

  task "gather" {
    objective = "Gather data"
    output = {
      summary    = string("Research summary", true)
      count      = number("Result count", true)
      categories = list(string, "Category names")
    }
  }
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			task := cfg.Missions[0].Tasks[0]
			Expect(task.Output).NotTo(BeNil())
			Expect(task.Output.Fields).To(HaveLen(3))

			summary := findOutputField(task.Output.Fields, "summary")
			Expect(summary.Type).To(Equal("string"))
			Expect(summary.Required).To(BeTrue())

			count := findOutputField(task.Output.Fields, "count")
			Expect(count.Type).To(Equal("number"))
			Expect(count.Required).To(BeTrue())

			cats := findOutputField(task.Output.Fields, "categories")
			Expect(cats.Type).To(Equal("array"))
			Expect(cats.Items).NotTo(BeNil())
			Expect(cats.Items.Type).To(Equal("string"))
		})

		It("still parses the verbose output block form", func() {
			hclSrc := fullBaseHCL() + `
mission "legacy" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]

  task "work" {
    objective = "Do work"
    output {
      field "result" {
        type     = "string"
        required = true
      }
    }
  }
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())
			output := cfg.Missions[0].Tasks[0].Output
			Expect(output).NotTo(BeNil())
			Expect(output.Fields).To(HaveLen(1))
			Expect(output.Fields[0].Name).To(Equal("result"))
		})
	})

	// ─────────────────────────────────────────────────────────────────────────
	// Dataset schema shorthand
	// ─────────────────────────────────────────────────────────────────────────

	Describe("dataset schema = { ... } shorthand", func() {

		It("parses dataset schema as object shorthand", func() {
			hclSrc := fullBaseHCL() + `
mission "iter" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]

  dataset "words" {
    items = [
      { word = "hello", count = 1 },
    ]
    schema = {
      word  = string("The word", true)
      count = number("Occurrence count", true)
    }
  }

  task "process" {
    objective = "Process words"
  }
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			datasets := cfg.Missions[0].Datasets
			Expect(datasets).To(HaveLen(1))
			Expect(datasets[0].Schema).NotTo(BeNil())
			Expect(datasets[0].Schema.Fields).To(HaveLen(2))

			word := findInputField(datasets[0].Schema.Fields, "word")
			Expect(word.Type).To(Equal("string"))
			Expect(word.Required).To(BeTrue())

			count := findInputField(datasets[0].Schema.Fields, "count")
			Expect(count.Type).To(Equal("number"))
		})
	})

	// ─────────────────────────────────────────────────────────────────────────
	// Mission inputs shorthand
	// ─────────────────────────────────────────────────────────────────────────

	Describe("mission inputs = { ... } shorthand", func() {

		It("parses required inputs (no default)", func() {
			hclSrc := fullBaseHCL() + `
mission "triage" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]

  inputs = {
    complaint = string("The original complaint")
    severity  = string("Severity level")
  }

  task "classify" {
    objective = "Classify: ${inputs.complaint}"
  }
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			Expect(cfg.Missions[0].Inputs).To(HaveLen(2))
			complaint := findMissionInput(cfg.Missions[0].Inputs, "complaint")
			Expect(complaint).NotTo(BeNil())
			Expect(complaint.Type).To(Equal("string"))
			Expect(complaint.Description).To(Equal("The original complaint"))
			Expect(complaint.Default).To(BeNil())
			Expect(complaint.Secret).To(BeFalse())
		})

		It("parses input with default value", func() {
			hclSrc := fullBaseHCL() + `
mission "report" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]

  inputs = {
    severity = string("Severity level", { default = "high" })
    limit    = number("Max results",    { default = 10 })
  }

  task "run" {
    objective = "Run the report"
  }
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			severity := findMissionInput(cfg.Missions[0].Inputs, "severity")
			Expect(severity).NotTo(BeNil())
			Expect(severity.Default).NotTo(BeNil())
			Expect(severity.Default.AsString()).To(Equal("high"))

			limit := findMissionInput(cfg.Missions[0].Inputs, "limit")
			Expect(limit).NotTo(BeNil())
			Expect(limit.Default).NotTo(BeNil())
		})

		It("parses input with secret = true flag", func() {
			hclSrc := fullBaseHCL() + `
mission "ai_task" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]

  inputs = {
    api_key = string("OpenAI API key", { secret = true })
  }

  task "run" {
    objective = "Run with API key"
  }
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			apiKey := findMissionInput(cfg.Missions[0].Inputs, "api_key")
			Expect(apiKey).NotTo(BeNil())
			Expect(apiKey.Secret).To(BeTrue())
			Expect(apiKey.Value).To(BeNil())
		})

		It("still parses the verbose input block form", func() {
			hclSrc := fullBaseHCL() + `
mission "legacy" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.test_agent]

  input "severity" {
    type        = "string"
    description = "Severity level"
    default     = "high"
  }

  task "run" {
    objective = "Run the mission"
  }
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			Expect(cfg.Missions[0].Inputs).To(HaveLen(1))
			Expect(cfg.Missions[0].Inputs[0].Name).To(Equal("severity"))
			Expect(cfg.Missions[0].Inputs[0].Default).NotTo(BeNil())
		})
	})

	// ─────────────────────────────────────────────────────────────────────────
	// ToAIToolsSchema nested conversion
	// ─────────────────────────────────────────────────────────────────────────

	Describe("ToAIToolsSchema recursive conversion", func() {

		It("converts nested list with object items to aitools schema", func() {
			hclSrc := minimalVarsHCL() + minimalModelHCL() + `
tool "order" {
  implements = builtins.http.post
  inputs = {
    items = list(object({
      name = string("Item name", true)
      qty  = number("Quantity")
    }), "Order items", true)
  }
  url  = "https://example.com/orders"
  body = { items = inputs.items }
}
`
			_, f := writeFixture("config.hcl", hclSrc)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			schema := cfg.CustomTools[0].Inputs.ToAIToolsSchema()

			itemsProp, ok := schema.Properties["items"]
			Expect(ok).To(BeTrue())
			Expect(string(itemsProp.Type)).To(Equal("array"))
			Expect(itemsProp.Items).NotTo(BeNil())
			Expect(string(itemsProp.Items.Type)).To(Equal("object"))
			Expect(itemsProp.Items.Properties).To(HaveKey("name"))
			Expect(itemsProp.Items.Properties).To(HaveKey("qty"))
			// Required fields on the nested object
			Expect(itemsProp.Items.Required).To(ConsistOf("name"))
		})
	})
})

// ─────────────────────────────────────────────────────────────────────────────
// Test helpers
// ─────────────────────────────────────────────────────────────────────────────

func findInputField(fields []config.InputField, name string) *config.InputField {
	for i := range fields {
		if fields[i].Name == name {
			return &fields[i]
		}
	}
	return nil
}

func findOutputField(fields []config.OutputField, name string) *config.OutputField {
	for i := range fields {
		if fields[i].Name == name {
			return &fields[i]
		}
	}
	return nil
}

func findMissionInput(inputs []config.MissionInput, name string) *config.MissionInput {
	for i := range inputs {
		if inputs[i].Name == name {
			return &inputs[i]
		}
	}
	return nil
}

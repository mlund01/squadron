package config_test

import (
	"squadron/config"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// schema shorthand tests: verify that the new { field = string("desc", true) } syntax
// produces identical structs to the verbose block syntax.

var _ = Describe("Schema shorthand syntax", func() {

	// ── tool inputs ───────────────────────────────────────────────────────────

	Describe("tool inputs shorthand", func() {
		It("parses primitive inputs via shorthand attribute", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
tool "weather" {
  implements  = builtins.http.get
  description = "Get weather"
  inputs = {
    city  = string("Target city", true)
    units = string("Temperature units")
    limit = number("Max results")
  }
  url = "https://wttr.in/${inputs.city}?format=3"
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			tool := cfg.CustomTools[0]
			Expect(tool.Inputs).NotTo(BeNil())
			fields := tool.Inputs.Fields

			// Shorthand produces sorted fields (alphabetical by name)
			Expect(fields).To(HaveLen(3))

			city := fieldByName(fields, "city")
			Expect(city.Type).To(Equal("string"))
			Expect(city.Description).To(Equal("Target city"))
			Expect(city.Required).To(BeTrue())

			limit := fieldByName(fields, "limit")
			Expect(limit.Type).To(Equal("number"))
			Expect(limit.Description).To(Equal("Max results"))
			Expect(limit.Required).To(BeFalse())

			units := fieldByName(fields, "units")
			Expect(units.Type).To(Equal("string"))
			Expect(units.Description).To(Equal("Temperature units"))
			Expect(units.Required).To(BeFalse())
		})

		It("shorthand and verbose block produce the same InputField structs", func() {
			shorthand := minimalVarsHCL() + minimalModelHCL() + `
tool "weather" {
  implements = builtins.http.get
  inputs = {
    city = string("City name", true)
  }
  url = "https://wttr.in/${inputs.city}?format=3"
}
`
			verbose := minimalVarsHCL() + minimalModelHCL() + `
tool "weather" {
  implements = builtins.http.get
  inputs {
    field "city" {
      type        = "string"
      description = "City name"
      required    = true
    }
  }
  url = "https://wttr.in/${inputs.city}?format=3"
}
`
			_, sf := writeFixture("s.hcl", shorthand)
			_, vf := writeFixture("v.hcl", verbose)

			sCfg, err := config.LoadFile(sf)
			Expect(err).NotTo(HaveOccurred())
			vCfg, err := config.LoadFile(vf)
			Expect(err).NotTo(HaveOccurred())

			Expect(sCfg.CustomTools[0].Inputs.Fields).To(Equal(vCfg.CustomTools[0].Inputs.Fields))
		})

		It("parses list type for tool inputs", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
tool "search" {
  implements = builtins.http.get
  inputs = {
    tags  = list(string, "Search tags", true)
    query = string("Search query", true)
  }
  url = "https://api.example.com/search?q=${inputs.query}"
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			fields := cfg.CustomTools[0].Inputs.Fields
			tags := fieldByName(fields, "tags")
			Expect(tags.Type).To(Equal("array"))
			Expect(tags.Required).To(BeTrue())
			Expect(tags.Items).NotTo(BeNil())
			Expect(tags.Items.Type).To(Equal("string"))
		})

		It("parses nested object type for tool inputs", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
tool "geo" {
  implements = builtins.http.get
  inputs = {
    coords = object({
      lat = number("Latitude",  true)
      lon = number("Longitude", true)
    }, "Geographic coordinates", true)
  }
  url = "https://api.example.com/geo"
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			fields := cfg.CustomTools[0].Inputs.Fields
			coords := fieldByName(fields, "coords")
			Expect(coords.Type).To(Equal("object"))
			Expect(coords.Required).To(BeTrue())
			Expect(coords.Properties).To(HaveLen(2))

			lat := fieldByName(coords.Properties, "lat")
			Expect(lat.Type).To(Equal("number"))
			Expect(lat.Required).To(BeTrue())
		})
	})

	// ── task output ───────────────────────────────────────────────────────────

	Describe("task output shorthand", func() {
		It("parses output fields via shorthand attribute", func() {
			hcl := fullBaseHCL() + `
mission "research" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  task "gather" {
    objective = "Gather data"
    output = {
      summary = string("Research summary", true)
      count   = number("Result count", true)
    }
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			output := cfg.Missions[0].Tasks[0].Output
			Expect(output).NotTo(BeNil())
			Expect(output.Fields).To(HaveLen(2))

			count := outputFieldByName(output.Fields, "count")
			Expect(count.Type).To(Equal("number"))
			Expect(count.Required).To(BeTrue())

			summary := outputFieldByName(output.Fields, "summary")
			Expect(summary.Type).To(Equal("string"))
			Expect(summary.Required).To(BeTrue())
		})

		It("shorthand output and verbose output block produce the same OutputField structs", func() {
			shorthand := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  task "t" {
    objective = "Do"
    output = {
      result = string("The result", true)
    }
  }
}
`
			verbose := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  task "t" {
    objective = "Do"
    output {
      field "result" {
        type        = "string"
        description = "The result"
        required    = true
      }
    }
  }
}
`
			_, sf := writeFixture("s.hcl", shorthand)
			_, vf := writeFixture("v.hcl", verbose)

			sCfg, err := config.LoadFile(sf)
			Expect(err).NotTo(HaveOccurred())
			vCfg, err := config.LoadFile(vf)
			Expect(err).NotTo(HaveOccurred())

			Expect(sCfg.Missions[0].Tasks[0].Output.Fields).To(Equal(vCfg.Missions[0].Tasks[0].Output.Fields))
		})

		It("parses list output field", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  task "t" {
    objective = "Do"
    output = {
      categories = list(string, "Category names")
    }
  }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			fields := cfg.Missions[0].Tasks[0].Output.Fields
			cat := outputFieldByName(fields, "categories")
			Expect(cat.Type).To(Equal("array"))
			Expect(cat.Items).NotTo(BeNil())
			Expect(cat.Items.Type).To(Equal("string"))
		})
	})

	// ── dataset schema ────────────────────────────────────────────────────────

	Describe("dataset schema shorthand", func() {
		It("parses schema fields via shorthand attribute", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  dataset "items" {
    schema = {
      id   = number("Item ID",  true)
      word = string("The word", true)
    }
    items = [
      { id = 1, word = "apple" },
      { id = 2, word = "banana" },
    ]
  }
  task "t" { objective = "Process" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			Expect(cfg.Missions[0].Datasets).To(HaveLen(1))
			schema := cfg.Missions[0].Datasets[0].Schema
			Expect(schema).NotTo(BeNil())
			Expect(schema.Fields).To(HaveLen(2))

			id := fieldByName(schema.Fields, "id")
			Expect(id.Type).To(Equal("number"))
			Expect(id.Required).To(BeTrue())

			word := fieldByName(schema.Fields, "word")
			Expect(word.Type).To(Equal("string"))
			Expect(word.Required).To(BeTrue())
		})

		It("shorthand schema and verbose schema block produce the same fields", func() {
			shorthand := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  dataset "items" {
    schema = {
      name = string("Item name", true)
    }
  }
  task "t" { objective = "Process" }
}
`
			verbose := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  dataset "items" {
    schema {
      field "name" {
        type        = "string"
        description = "Item name"
        required    = true
      }
    }
  }
  task "t" { objective = "Process" }
}
`
			_, sf := writeFixture("s.hcl", shorthand)
			_, vf := writeFixture("v.hcl", verbose)

			sCfg, err := config.LoadFile(sf)
			Expect(err).NotTo(HaveOccurred())
			vCfg, err := config.LoadFile(vf)
			Expect(err).NotTo(HaveOccurred())

			Expect(sCfg.Missions[0].Datasets[0].Schema.Fields).To(Equal(vCfg.Missions[0].Datasets[0].Schema.Fields))
		})
	})

	// ── mission inputs ────────────────────────────────────────────────────────

	Describe("mission inputs shorthand", func() {
		It("parses inputs via shorthand attribute", func() {
			hcl := fullBaseHCL() + `
mission "triage" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  inputs = {
    complaint = string("The original complaint", true)
    severity  = string("Severity level")
    threshold = number("Score threshold")
  }
  task "t" { objective = "Process ${inputs.complaint}" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			inputs := cfg.Missions[0].Inputs
			Expect(inputs).To(HaveLen(3))

			complaint := missionInputByName(inputs, "complaint")
			Expect(complaint.Type).To(Equal("string"))
			Expect(complaint.Description).To(Equal("The original complaint"))

			severity := missionInputByName(inputs, "severity")
			Expect(severity.Type).To(Equal("string"))
		})

		It("parses mission input with default value", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  inputs = {
    severity  = string("Severity level", { default = "high" })
    threshold = number("Score threshold", { default = 0.75 })
  }
  task "t" { objective = "Process" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			inputs := cfg.Missions[0].Inputs

			sev := missionInputByName(inputs, "severity")
			Expect(sev.Default).NotTo(BeNil())

			thr := missionInputByName(inputs, "threshold")
			Expect(thr.Default).NotTo(BeNil())
		})

		It("parses mission input with secret = true flag", func() {
			hcl := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  inputs = {
    api_key = string("OpenAI API key", { secret = true })
  }
  task "t" { objective = "Call API" }
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			inputs := cfg.Missions[0].Inputs
			apiKey := missionInputByName(inputs, "api_key")
			Expect(apiKey.Secret).To(BeTrue())
			Expect(apiKey.Value).To(BeNil())
		})

		It("shorthand inputs and verbose input blocks produce equivalent results", func() {
			shorthand := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  inputs = {
    target = string("Target URL", true)
    count  = number("Item count")
  }
  task "t" { objective = "Process" }
}
`
			verbose := fullBaseHCL() + `
mission "m" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents = [agents.test_agent]
  input "target" {
    type        = "string"
    description = "Target URL"
  }
  input "count" {
    type        = "number"
    description = "Item count"
  }
  task "t" { objective = "Process" }
}
`
			_, sf := writeFixture("s.hcl", shorthand)
			_, vf := writeFixture("v.hcl", verbose)

			sCfg, err := config.LoadFile(sf)
			Expect(err).NotTo(HaveOccurred())
			vCfg, err := config.LoadFile(vf)
			Expect(err).NotTo(HaveOccurred())

			sInputs := sCfg.Missions[0].Inputs
			vInputs := vCfg.Missions[0].Inputs
			Expect(sInputs).To(HaveLen(len(vInputs)))

			// Match by name since order may differ (shorthand is sorted alphabetically)
			for _, vi := range vInputs {
				si := missionInputByName(sInputs, vi.Name)
				Expect(si.Type).To(Equal(vi.Type))
				Expect(si.Description).To(Equal(vi.Description))
			}
		})
	})

	// ── ToAIToolsSchema recursive conversion ──────────────────────────────────

	Describe("ToAIToolsSchema with nested types", func() {
		It("converts list field with items to aitools schema", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
tool "search" {
  implements = builtins.http.get
  inputs = {
    tags = list(string, "Search tags", true)
  }
  url = "https://api.example.com"
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			schema := cfg.CustomTools[0].Inputs.ToAIToolsSchema()
			Expect(schema.Properties).To(HaveKey("tags"))
			tagsProp := schema.Properties["tags"]
			Expect(string(tagsProp.Type)).To(Equal("array"))
			Expect(tagsProp.Items).NotTo(BeNil())
			Expect(string(tagsProp.Items.Type)).To(Equal("string"))
			Expect(schema.Required).To(ContainElement("tags"))
		})

		It("converts nested object field to aitools schema", func() {
			hcl := minimalVarsHCL() + minimalModelHCL() + `
tool "geo" {
  implements = builtins.http.get
  inputs = {
    coords = object({
      lat = number("Latitude",  true)
      lon = number("Longitude", true)
    }, "Coordinates", true)
  }
  url = "https://api.example.com"
}
`
			_, f := writeFixture("config.hcl", hcl)
			cfg, err := config.LoadFile(f)
			Expect(err).NotTo(HaveOccurred())

			schema := cfg.CustomTools[0].Inputs.ToAIToolsSchema()
			Expect(schema.Properties).To(HaveKey("coords"))
			coords := schema.Properties["coords"]
			Expect(string(coords.Type)).To(Equal("object"))
			Expect(coords.Properties).To(HaveKey("lat"))
			Expect(coords.Properties).To(HaveKey("lon"))
			Expect(coords.Required).To(ConsistOf("lat", "lon"))
		})
	})
})

// ── test helpers ──────────────────────────────────────────────────────────────

func fieldByName(fields []config.InputField, name string) config.InputField {
	for _, f := range fields {
		if f.Name == name {
			return f
		}
	}
	Fail("field " + name + " not found")
	return config.InputField{}
}

func outputFieldByName(fields []config.OutputField, name string) config.OutputField {
	for _, f := range fields {
		if f.Name == name {
			return f
		}
	}
	Fail("output field " + name + " not found")
	return config.OutputField{}
}

func missionInputByName(inputs []config.MissionInput, name string) config.MissionInput {
	for _, i := range inputs {
		if i.Name == name {
			return i
		}
	}
	Fail("mission input " + name + " not found")
	return config.MissionInput{}
}

package mcp

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	mcpproto "github.com/mark3labs/mcp-go/mcp"
)

var _ = Describe("convertSchema raw passthrough", func() {
	It("preserves enum in the raw JSON sent to the LLM", func() {
		in := mcpproto.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"status": map[string]any{
					"type": "string",
					"enum": []any{"open", "closed", "in_progress"},
				},
			},
			Required: []string{"status"},
		}

		out := convertSchema(in)
		raw := out.ToJSONSchema()

		var parsed map[string]any
		Expect(json.Unmarshal(raw, &parsed)).To(Succeed())

		props := parsed["properties"].(map[string]any)
		status := props["status"].(map[string]any)
		Expect(status).To(HaveKey("enum"))
	})

	It("preserves additionalProperties in the raw JSON", func() {
		in := mcpproto.ToolInputSchema{
			Type:                 "object",
			Properties:           map[string]any{"name": map[string]any{"type": "string"}},
			AdditionalProperties: false,
		}

		out := convertSchema(in)
		raw := out.ToJSONSchema()

		var parsed map[string]any
		Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
		Expect(parsed).To(HaveKey("additionalProperties"))
	})

	It("preserves $defs in the raw JSON", func() {
		in := mcpproto.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{},
			Defs: map[string]any{
				"Color": map[string]any{"type": "string", "enum": []any{"red", "blue"}},
			},
		}

		out := convertSchema(in)
		raw := out.ToJSONSchema()

		var parsed map[string]any
		Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
		Expect(parsed).To(HaveKey("$defs"))
	})

	It("still populates typed fields for internal use", func() {
		in := mcpproto.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{"type": "string", "description": "search query"},
			},
			Required: []string{"query"},
		}

		out := convertSchema(in)
		Expect(string(out.Type)).To(Equal("object"))
		Expect(out.Properties).To(HaveKey("query"))
		Expect(out.Required).To(ContainElement("query"))
	})

	It("handles nullable type arrays gracefully", func() {
		in := mcpproto.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"label": map[string]any{
					"type": []any{"string", "null"},
				},
			},
		}

		out := convertSchema(in)
		// Typed projection can't handle array types — should degrade gracefully
		Expect(string(out.Type)).To(Equal("object"))
		Expect(out.Properties).NotTo(BeNil())

		// Raw passthrough should still contain the array type
		raw := out.ToJSONSchema()
		var parsed map[string]any
		Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
		props := parsed["properties"].(map[string]any)
		label := props["label"].(map[string]any)
		Expect(label["type"]).To(BeAssignableToTypeOf([]any{}))
	})
})

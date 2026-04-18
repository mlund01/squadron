package llm

import (
	"encoding/json"
	"testing"

	"google.golang.org/genai"
)

func TestJSONSchemaToGemini_BasicObject(t *testing.T) {
	raw := `{
		"type": "object",
		"description": "a thing",
		"properties": {
			"name": {"type": "string", "description": "the name"},
			"count": {"type": "integer", "minimum": 0, "maximum": 100}
		},
		"required": ["name"]
	}`
	s := parseSchema(t, raw)

	if s.Type != genai.TypeObject {
		t.Fatalf("type = %q, want OBJECT", s.Type)
	}
	if s.Description != "a thing" {
		t.Errorf("description = %q", s.Description)
	}
	if s.Properties["name"].Type != genai.TypeString {
		t.Errorf("name type = %q", s.Properties["name"].Type)
	}
	if got := s.Properties["count"]; got.Minimum == nil || *got.Minimum != 0 {
		t.Errorf("count.minimum = %v, want 0", got.Minimum)
	}
	if got := s.Properties["count"]; got.Maximum == nil || *got.Maximum != 100 {
		t.Errorf("count.maximum = %v, want 100", got.Maximum)
	}
	if len(s.Required) != 1 || s.Required[0] != "name" {
		t.Errorf("required = %v", s.Required)
	}
}

func TestJSONSchemaToGemini_StringEnum(t *testing.T) {
	raw := `{"type": "string", "enum": ["east", "west", "north", "south"]}`
	s := parseSchema(t, raw)

	if s.Type != genai.TypeString {
		t.Fatalf("type = %q", s.Type)
	}
	if s.Format != "enum" {
		t.Errorf("format = %q, want enum", s.Format)
	}
	if len(s.Enum) != 4 || s.Enum[0] != "east" {
		t.Errorf("enum = %v", s.Enum)
	}
}

func TestJSONSchemaToGemini_IntEnumStringified(t *testing.T) {
	raw := `{"type": "integer", "enum": [101, 201, 301]}`
	s := parseSchema(t, raw)

	if s.Type != genai.TypeInteger {
		t.Fatalf("type = %q", s.Type)
	}
	if s.Format != "enum" {
		t.Errorf("format = %q", s.Format)
	}
	want := []string{"101", "201", "301"}
	if len(s.Enum) != 3 {
		t.Fatalf("enum len = %d, want 3", len(s.Enum))
	}
	for i, v := range want {
		if s.Enum[i] != v {
			t.Errorf("enum[%d] = %q, want %q", i, s.Enum[i], v)
		}
	}
}

func TestJSONSchemaToGemini_NullableUnionType(t *testing.T) {
	raw := `{"type": ["string", "null"], "description": "optional label"}`
	s := parseSchema(t, raw)

	if s.Type != genai.TypeString {
		t.Errorf("type = %q, want STRING", s.Type)
	}
	if s.Nullable == nil || !*s.Nullable {
		t.Errorf("nullable = %v, want true", s.Nullable)
	}
}

func TestJSONSchemaToGemini_AnyOf(t *testing.T) {
	raw := `{"anyOf": [{"type": "string"}, {"type": "integer"}], "description": "flexible"}`
	s := parseSchema(t, raw)

	if len(s.AnyOf) != 2 {
		t.Fatalf("anyOf len = %d", len(s.AnyOf))
	}
	if s.AnyOf[0].Type != genai.TypeString || s.AnyOf[1].Type != genai.TypeInteger {
		t.Errorf("anyOf types: %q, %q", s.AnyOf[0].Type, s.AnyOf[1].Type)
	}
}

func TestJSONSchemaToGemini_OneOfMapsToAnyOf(t *testing.T) {
	raw := `{"oneOf": [{"type": "string"}, {"type": "boolean"}]}`
	s := parseSchema(t, raw)

	if len(s.AnyOf) != 2 {
		t.Fatalf("anyOf len = %d, want 2 (oneOf maps to anyOf)", len(s.AnyOf))
	}
}

func TestJSONSchemaToGemini_StringConstraints(t *testing.T) {
	raw := `{"type": "string", "format": "email", "pattern": ".+@.+", "minLength": 3, "maxLength": 320}`
	s := parseSchema(t, raw)

	if s.Format != "email" || s.Pattern != ".+@.+" {
		t.Errorf("format/pattern = %q/%q", s.Format, s.Pattern)
	}
	if s.MinLength == nil || *s.MinLength != 3 {
		t.Errorf("minLength = %v", s.MinLength)
	}
	if s.MaxLength == nil || *s.MaxLength != 320 {
		t.Errorf("maxLength = %v", s.MaxLength)
	}
}

func TestJSONSchemaToGemini_ArrayConstraints(t *testing.T) {
	raw := `{"type": "array", "items": {"type": "string"}, "minItems": 1, "maxItems": 5}`
	s := parseSchema(t, raw)

	if s.Type != genai.TypeArray || s.Items == nil || s.Items.Type != genai.TypeString {
		t.Fatalf("array schema incorrect: %+v", s)
	}
	if s.MinItems == nil || *s.MinItems != 1 {
		t.Errorf("minItems = %v", s.MinItems)
	}
	if s.MaxItems == nil || *s.MaxItems != 5 {
		t.Errorf("maxItems = %v", s.MaxItems)
	}
}

func TestJSONSchemaToGemini_NestedProperties(t *testing.T) {
	raw := `{
		"type": "object",
		"properties": {
			"user": {
				"type": "object",
				"properties": {
					"role": {"type": "string", "enum": ["admin", "member"]}
				},
				"required": ["role"]
			}
		}
	}`
	s := parseSchema(t, raw)

	user := s.Properties["user"]
	if user == nil || user.Type != genai.TypeObject {
		t.Fatalf("user schema = %+v", user)
	}
	role := user.Properties["role"]
	if role == nil || len(role.Enum) != 2 {
		t.Fatalf("role enum = %v", role)
	}
}

func parseSchema(t *testing.T, raw string) *genai.Schema {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return jsonSchemaToGeminiSchema(m)
}

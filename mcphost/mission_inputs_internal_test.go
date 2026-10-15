package mcphost

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"squadron/config"
)

func TestMissionInputToJSONReportsRequiredAndDefault(t *testing.T) {
	def := cty.StringVal("https://github.com/FedTax/x")
	secret := cty.StringVal("s3cret")
	got := missionInputsJSON([]config.MissionInput{
		{Name: "issue", Type: "string"},
		{Name: "repo_url", Type: "string", Default: &def},
		{Name: "token", Type: "string", Protected: true, Default: &secret},
	})
	if !got[0].Required || got[0].Default != nil {
		t.Errorf("issue: %+v", got[0])
	}
	if got[1].Required || string(got[1].Default) != `"https://github.com/FedTax/x"` {
		t.Errorf("repo_url: required=%v default=%s", got[1].Required, got[1].Default)
	}
	if got[2].Required || got[2].Default != nil {
		t.Errorf("a protected input must not be required or leak its default: %+v", got[2])
	}
}

func TestMissionInputToJSONNestedMetadata(t *testing.T) {
	secret := cty.StringVal("secret")
	zero := cty.NumberIntVal(0)
	unknown := cty.UnknownVal(cty.String)
	got := missionInputToJSON(config.MissionInput{
		Name: "rows", Type: "list", Items: &config.MissionInput{
			Type: "object", Properties: []config.MissionInput{
				{Name: "token", Type: "string", Protected: true, Default: &secret},
				{Name: "count", Type: "integer", Default: &zero},
				{Name: "pending", Type: "string", Default: &unknown},
			},
		},
	})
	if got.Items == nil || len(got.Items.Properties) != 3 {
		t.Fatalf("missing nested properties: %+v", got)
	}
	props := got.Items.Properties
	if props[0].Default != nil || !props[0].Protected || props[0].Required {
		t.Fatal("nested protected default exposed or marked required")
	}
	if string(props[1].Default) != "0" || props[1].Required {
		t.Fatal("zero default lost")
	}
	if props[2].Default != nil {
		t.Fatal("unknown default must be omitted")
	}
}

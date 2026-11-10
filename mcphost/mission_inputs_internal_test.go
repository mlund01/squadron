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

package config

import (
	"strings"
	"testing"
)

func TestValidateBlockName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		// valid
		{"agent", false},
		{"a", false},
		{"snake_case", false},
		{"_leading_underscore", false},
		{"trailing_digits_42", false},
		{"task1", false},
		{"v1", false},
		{"a1b2c3", false},
		{"________", false},

		// invalid: empty
		{"", true},

		// invalid: leading digit
		{"1key", true},
		{"2fast", true},

		// invalid: uppercase
		{"Agent", true},
		{"camelCase", true},
		{"ALLCAPS", true},

		// invalid: hyphen / space / punctuation
		{"test-agent", true},
		{"test agent", true},
		{"agent.name", true},
		{"agent/name", true},
		{"naïve", true},
		{"emoji_😀", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBlockName("block", tc.name)
			if tc.wantErr && err == nil {
				t.Fatalf("validateBlockName(%q): expected error, got nil", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateBlockName(%q): unexpected error: %v", tc.name, err)
			}
			// Error message should name the offending block kind + label.
			if tc.wantErr && tc.name != "" {
				if !strings.Contains(err.Error(), "block name") {
					t.Errorf("validateBlockName(%q): error %q missing block kind", tc.name, err)
				}
				if !strings.Contains(err.Error(), tc.name) {
					t.Errorf("validateBlockName(%q): error %q missing offending name", tc.name, err)
				}
			}
		})
	}
}

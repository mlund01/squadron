package config

import (
	"strings"
	"testing"
)

type fakeConfigurer struct {
	calls int
	last  map[string]string
}

func (f *fakeConfigurer) Configure(settings map[string]string) error {
	f.calls++
	f.last = settings
	return nil
}

// configurePlugin must always invoke Configure, even when Settings is nil or
// empty — some plugins require it to initialize internal state and will refuse
// every subsequent tool call with "plugin not configured" otherwise.
func TestConfigurePlugin_AlwaysInvokesConfigure(t *testing.T) {
	cases := []struct {
		name     string
		settings map[string]string
	}{
		{"nil settings", nil},
		{"empty settings", map[string]string{}},
		{"populated settings", map[string]string{"k": "v"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &fakeConfigurer{}
			if err := configurePlugin(fc, "myplugin", tc.settings); err != nil {
				t.Fatalf("configurePlugin returned unexpected error: %v", err)
			}
			if fc.calls != 1 {
				t.Fatalf("Configure call count = %d, want 1", fc.calls)
			}
		})
	}
}

func TestConfigurePlugin_RejectsEmptyValues(t *testing.T) {
	fc := &fakeConfigurer{}
	err := configurePlugin(fc, "myplugin", map[string]string{"api_key": ""})
	if err == nil {
		t.Fatal("expected error for empty setting value, got nil")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Errorf("error %q should mention the empty setting key 'api_key'", err.Error())
	}
	if fc.calls != 0 {
		t.Errorf("Configure must not be called when validation fails; got %d calls", fc.calls)
	}
}


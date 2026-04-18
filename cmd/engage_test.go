package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasHCLFiles(t *testing.T) {
	t.Run("empty dir", func(t *testing.T) {
		if hasHCLFiles(t.TempDir()) {
			t.Error("empty dir should not have HCL files")
		}
	})

	t.Run("dir with hcl", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "squadron.hcl"), []byte(""), 0644); err != nil {
			t.Fatal(err)
		}
		if !hasHCLFiles(dir) {
			t.Error("dir with HCL should be detected")
		}
	})

	t.Run("dir with non-hcl", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(""), 0644); err != nil {
			t.Fatal(err)
		}
		if hasHCLFiles(dir) {
			t.Error("dir without HCL should not be detected")
		}
	})

	t.Run("file path", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "my.hcl")
		if err := os.WriteFile(file, []byte(""), 0644); err != nil {
			t.Fatal(err)
		}
		if !hasHCLFiles(file) {
			t.Error("HCL file path should be detected")
		}
	})

	t.Run("nonexistent path", func(t *testing.T) {
		if hasHCLFiles(filepath.Join(t.TempDir(), "does-not-exist")) {
			t.Error("nonexistent path should not be detected")
		}
	})
}

func TestConfigHasCommandCenter(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "declared",
			content: `command_center {` + "\n  host = \"https://example.com\"\n}\n",
			want:    true,
		},
		{
			name: "indented declared",
			content: `
  command_center {
    host = "https://example.com"
  }
`,
			want: true,
		},
		{
			name: "hash commented out",
			content: `# command_center {
#   host = "https://example.com"
# }
variable "x" { secret = true }
`,
			want: false,
		},
		{
			name: "slash commented out",
			content: `// command_center {
//   host = "https://example.com"
// }
`,
			want: false,
		},
		{
			name: "not present",
			content: `variable "x" { secret = true }
model "foo" { provider = "anthropic" }
`,
			want: false,
		},
		{
			name: "command_center as substring",
			content: `variable "command_center_url" { secret = true }`,
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "squadron.hcl"), []byte(tc.content), 0644); err != nil {
				t.Fatal(err)
			}
			if got := configHasCommandCenter(dir); got != tc.want {
				t.Errorf("configHasCommandCenter = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateConfigDir_CleanDir(t *testing.T) {
	dir := t.TempDir()
	warning, err := validateConfigDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warning != "" {
		t.Errorf("unexpected warning for clean tempdir: %q", warning)
	}
}

func TestValidateConfigDir_NestedSquadron(t *testing.T) {
	dir := t.TempDir()
	nestedProject := filepath.Join(dir, "sub", "project")
	if err := os.MkdirAll(filepath.Join(nestedProject, ".squadron"), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := validateConfigDir(dir)
	if err == nil {
		t.Fatal("expected error for nested .squadron directory")
	}
}

func TestValidateConfigDir_SelfSquadronIsFine(t *testing.T) {
	dir := t.TempDir()
	// A .squadron directory in the config dir itself (not nested) is fine.
	if err := os.MkdirAll(filepath.Join(dir, ".squadron"), 0755); err != nil {
		t.Fatal(err)
	}

	warning, err := validateConfigDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warning != "" {
		t.Errorf("unexpected warning: %q", warning)
	}
}

func TestValidateConfigDir_HighLevelWarning(t *testing.T) {
	// We can't actually test running from `/` or `~`, so verify the tmp dir
	// doesn't trigger a warning (negative test).
	warning, err := validateConfigDir(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if warning != "" {
		t.Errorf("tempdir should not produce a warning, got: %q", warning)
	}
}

func TestIsContainer(t *testing.T) {
	t.Setenv("SQUADRON_CONTAINER", "1")
	if !isContainer() {
		t.Error("isContainer() should be true with SQUADRON_CONTAINER=1")
	}

	t.Setenv("SQUADRON_CONTAINER", "")
	if isContainer() {
		t.Error("isContainer() should be false with SQUADRON_CONTAINER unset")
	}

	t.Setenv("SQUADRON_CONTAINER", "0")
	if isContainer() {
		t.Error("isContainer() should be false with SQUADRON_CONTAINER=0")
	}
}

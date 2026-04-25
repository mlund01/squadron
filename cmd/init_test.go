package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSquadronGitignored(t *testing.T) {
	cases := []struct {
		name     string
		existing *string // nil means file does not exist
		want     string
	}{
		{
			name:     "no file",
			existing: nil,
			want:     ".squadron/\n",
		},
		{
			name:     "empty file",
			existing: ptr(""),
			want:     ".squadron/\n",
		},
		{
			name:     "existing ends with newline",
			existing: ptr("node_modules/\n"),
			want:     "node_modules/\n.squadron/\n",
		},
		{
			name:     "existing missing trailing newline",
			existing: ptr("node_modules/"),
			want:     "node_modules/\n.squadron/\n",
		},
		{
			name:     "existing multi-line missing trailing newline",
			existing: ptr("node_modules/\ndist/"),
			want:     "node_modules/\ndist/\n.squadron/\n",
		},
		{
			name:     "already contains .squadron",
			existing: ptr("node_modules/\n.squadron\n"),
			want:     "node_modules/\n.squadron\n",
		},
		{
			name:     "already contains .squadron/",
			existing: ptr(".squadron/\n"),
			want:     ".squadron/\n",
		},
		{
			name:     "already contains /.squadron/",
			existing: ptr("/.squadron/\n"),
			want:     "/.squadron/\n",
		},
		{
			name:     "comment matching entry is ignored",
			existing: ptr("# .squadron\n"),
			want:     "# .squadron\n.squadron/\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatalf("getwd: %v", err)
			}
			if err := os.Chdir(dir); err != nil {
				t.Fatalf("chdir: %v", err)
			}
			t.Cleanup(func() { _ = os.Chdir(cwd) })

			gitignorePath := filepath.Join(dir, ".gitignore")
			if tc.existing != nil {
				if err := os.WriteFile(gitignorePath, []byte(*tc.existing), 0644); err != nil {
					t.Fatalf("seed gitignore: %v", err)
				}
			}

			if err := ensureSquadronGitignored(); err != nil {
				t.Fatalf("ensureSquadronGitignored: %v", err)
			}

			got, err := os.ReadFile(gitignorePath)
			if err != nil {
				t.Fatalf("read gitignore: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("gitignore mismatch\ngot:  %q\nwant: %q", string(got), tc.want)
			}
		})
	}
}

func TestGitignoreContains(t *testing.T) {
	cases := []struct {
		data string
		want bool
	}{
		{"", false},
		{".squadron\n", true},
		{".squadron/\n", true},
		{"/.squadron\n", true},
		{"/.squadron/\n", true},
		{"node_modules/\n.squadron/\n", true},
		{"# .squadron\n", false},
		{"  .squadron  \n", true},
		{".squadron-other/\n", false},
		{"squadron/\n", false},
	}

	for _, tc := range cases {
		got := gitignoreContains([]byte(tc.data), ".squadron")
		if got != tc.want {
			t.Errorf("gitignoreContains(%q) = %v, want %v", tc.data, got, tc.want)
		}
	}
}

func ptr(s string) *string { return &s }

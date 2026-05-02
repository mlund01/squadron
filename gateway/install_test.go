package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureInstalled covers both happy and sad paths without touching the
// network: the happy path drops a fake binary into the cache layout and
// expects EnsureInstalled to short-circuit on the os.Stat hit; the sad path
// asks for a version with no cached binary AND no source so EnsureInstalled
// errors out before attempting any download.
//
// Both subtests share one SQUADRON_HOME because internal/paths caches the
// resolved home in a sync.Once. As long as no prior test in this package has
// called paths.SquadronHome (verified by grep for SquadronHome in the other
// gateway/*_test.go files), t.Setenv on the parent test wins.
func TestEnsureInstalled(t *testing.T) {
	t.Setenv("SQUADRON_HOME", t.TempDir())

	t.Run("returns cached binary when already on disk", func(t *testing.T) {
		bin, err := gatewayBinary("cached", "v0.0.1")
		if err != nil {
			t.Fatalf("gatewayBinary: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(bin), 0755); err != nil {
			t.Fatalf("mkdir cache: %v", err)
		}
		if err := os.WriteFile(bin, []byte("fake"), 0755); err != nil {
			t.Fatalf("write fake binary: %v", err)
		}

		// Source intentionally non-empty to prove EnsureInstalled does NOT
		// reach the download path when the cache hits.
		got, err := EnsureInstalled("cached", "v0.0.1", "github.com/never/visited")
		if err != nil {
			t.Fatalf("EnsureInstalled: unexpected error: %v", err)
		}
		if got != bin {
			t.Fatalf("EnsureInstalled: got %q, want %q", got, bin)
		}
	})

	t.Run("errors when binary is missing and no source is configured", func(t *testing.T) {
		// Use a version that has no cached binary. Empty source mirrors the
		// HCL `version = "local"` case where the user is expected to have
		// pre-built the binary.
		_, err := EnsureInstalled("missing", "v9.9.9", "")
		if err == nil {
			t.Fatal("EnsureInstalled: want error, got nil")
		}
		if !strings.Contains(err.Error(), "not installed") {
			t.Fatalf("EnsureInstalled: error %q does not mention 'not installed'", err.Error())
		}
		if !strings.Contains(err.Error(), "no source configured") {
			t.Fatalf("EnsureInstalled: error %q does not mention 'no source configured'", err.Error())
		}
	})
}

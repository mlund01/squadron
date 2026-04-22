package repolint_test

import (
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// allowedExecutables is the explicit set of tracked files that are permitted
// to have the executable bit set. Anything else showing up with mode 100755
// fails the test — most often that means a compiled plugin binary got
// committed by accident. Add a new entry here only after deliberately
// deciding the file should be runnable on checkout.
var allowedExecutables = map[string]bool{
	"docker-entrypoint.sh": true,
	"install.sh":           true,
}

func TestNoUnexpectedExecutables(t *testing.T) {
	cmd := exec.Command("git", "ls-files", "-s")
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files failed: %v", err)
	}

	var offenders []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		// Format: "<mode> <sha> <stage>\t<path>"
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		meta, path := line[:tab], line[tab+1:]
		fields := strings.Fields(meta)
		if len(fields) < 1 || fields[0] != "100755" {
			continue
		}
		if allowedExecutables[path] {
			continue
		}
		offenders = append(offenders, path)
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf(
			"found %d tracked executable file(s) not in the allowlist:\n  %s\n\n"+
				"If one of these was committed by accident (e.g. a compiled binary), remove it with `git rm`.\n"+
				"If it is a legitimate executable, add it to allowedExecutables in internal/repolint/executables_test.go.",
			len(offenders), strings.Join(offenders, "\n  "),
		)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel failed: %v", err)
	}
	return strings.TrimSpace(string(out))
}

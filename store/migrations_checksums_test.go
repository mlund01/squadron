package store_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/store"
)

// migrationChecksums locks the content of every applied migration file.
//
// WHY THIS EXISTS
// Migrations are append-only: once a version has shipped, any user upgrading
// across it runs that exact SQL. Silently editing a migration file (even to
// "fix a typo") means users who already applied the old version will never
// see the fix, producing divergent schemas across deployments. This map is
// the tripwire: any change to a registered file breaks the build, forcing
// the author to add a NEW migration instead.
//
// RULES
//   - When you add a migration, also add its filename + sha256 here. The
//     test below fails if a file under store/migrations/ has no entry.
//   - NEVER edit an existing entry. If a migration is wrong, write a new
//     one that fixes it forward.
//   - NEVER delete an entry. If a migration file disappears from disk but
//     an entry remains here, the test fails — that is intentional, it
//     means a historical migration was removed.
//
// Compute a new checksum with:
//
//	shasum -a 256 store/migrations/<file>
var migrationChecksums = map[string]string{
	"0001_baseline.sqlite.sql":               "ea7a46271d90d7e19daff1b608d67137a091811dba1382f9f4267828c1d9ffa6",
	"0001_baseline.postgres.sql":             "ada6e4e89800425c2512877f20b090f22eacb8e9fa3aea3d8ebb94e953448c8f",
	"0002_human_input_requests.sqlite.sql":   "fba61b19e6012c83812b575426d16af148717c31c18bf225af4157a47ce55859",
	"0002_human_input_requests.postgres.sql": "65efa3f72f005b8b01424616115177da6bca365cabb1abc9824529755fe6e2ee",
	"0003_session_message_parts.sqlite.sql":   "40371e8a46c410ca7c06324d998ab1db2177a1011f2e1d6a7ac9ab3ca04c973d",
	"0003_session_message_parts.postgres.sql": "281190245e3a27f9cd4bf5feec9e973a5857a962d64e35caef8fef6440d6b8d9",
}

var _ = Describe("Migration checksums", func() {
	It("every migration file on disk has a recorded checksum, and every recorded checksum points to unchanged content", func() {
		mfs := store.MigrationFS()

		onDisk := map[string][]byte{}
		err := fs.WalkDir(mfs, ".", func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			body, err := fs.ReadFile(mfs, path)
			if err != nil {
				return err
			}
			onDisk[path] = body
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		// 1. Every file on disk must have a registered checksum.
		var missing []string
		for name := range onDisk {
			if _, ok := migrationChecksums[name]; !ok {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		Expect(missing).To(BeEmpty(),
			"migration files without a registered checksum — add them to migrationChecksums in migrations_checksums_test.go: %v",
			missing)

		// 2. Every registered checksum must point to a file still on disk.
		var vanished []string
		for name := range migrationChecksums {
			if _, ok := onDisk[name]; !ok {
				vanished = append(vanished, name)
			}
		}
		sort.Strings(vanished)
		Expect(vanished).To(BeEmpty(),
			"migrationChecksums references files that no longer exist — historical migrations must not be deleted: %v",
			vanished)

		// 3. Content must match the recorded checksum byte-for-byte.
		for name, body := range onDisk {
			expected, ok := migrationChecksums[name]
			if !ok {
				continue // already reported above
			}
			sum := sha256.Sum256(body)
			got := hex.EncodeToString(sum[:])
			Expect(got).To(Equal(expected),
				"migration file %s has been modified — migrations are append-only. Revert the change and add a NEW migration instead. (expected %s, got %s)",
				name, expected, got)
		}
	})
})

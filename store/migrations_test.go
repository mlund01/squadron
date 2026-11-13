package store_test

import (
	"database/sql"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	_ "modernc.org/sqlite"

	"squadron/store"
)

var _ = Describe("Migrations", func() {
	var db *sql.DB
	var cleanup func()

	BeforeEach(func() {
		dir, err := os.MkdirTemp("", "migrations-test-*")
		Expect(err).NotTo(HaveOccurred())
		dbPath := filepath.Join(dir, "test.db")

		db, err = sql.Open("sqlite", dbPath)
		Expect(err).NotTo(HaveOccurred())
		db.SetMaxOpenConns(1)

		cleanup = func() {
			db.Close()
			os.RemoveAll(dir)
		}
	})

	AfterEach(func() { cleanup() })

	It("creates schema_migrations and records every embedded migration on first run", func() {
		Expect(store.RunMigrations(db, store.DialectSQLite)).To(Succeed())

		rows, err := db.Query(`SELECT version, name FROM schema_migrations ORDER BY version`)
		Expect(err).NotTo(HaveOccurred())
		defer rows.Close()

		var versions []int
		for rows.Next() {
			var v int
			var n string
			Expect(rows.Scan(&v, &n)).To(Succeed())
			versions = append(versions, v)
		}

		loaded, err := store.LoadMigrations()
		Expect(err).NotTo(HaveOccurred())
		expected := make([]int, 0, len(loaded))
		for _, m := range loaded {
			expected = append(expected, m.Version)
		}
		Expect(versions).To(Equal(expected))
	})

	It("is idempotent — running twice does not duplicate rows or fail", func() {
		Expect(store.RunMigrations(db, store.DialectSQLite)).To(Succeed())
		Expect(store.RunMigrations(db, store.DialectSQLite)).To(Succeed())

		loaded, err := store.LoadMigrations()
		Expect(err).NotTo(HaveOccurred())

		var count int
		Expect(db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(len(loaded)))
	})

	It("upgrades a main-branch database that already has the thread column", func() {
		migrations, err := store.LoadMigrations()
		Expect(err).NotTo(HaveOccurred())
		Expect(migrations).To(HaveLen(7))
		_, err = db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`)
		Expect(err).NotTo(HaveOccurred())
		for _, migration := range migrations[:6] {
			sqlText := migration.SQLite
			name := migration.Name
			if migration.Version == 5 {
				// Main used version 5 for mission threads, not the branch's
				// reserved slot. Both paths must converge at version 7.
				sqlText = `ALTER TABLE missions ADD COLUMN thread_id TEXT NOT NULL DEFAULT ''`
				name = "mission_threads"
			}
			_, err = db.Exec(sqlText)
			Expect(err).NotTo(HaveOccurred())
			_, err = db.Exec(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, 'test')`, migration.Version, name)
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(store.RunMigrations(db, store.DialectSQLite)).To(Succeed())
		var count int
		Expect(db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = 7`).Scan(&count)).To(Succeed())
		Expect(count).To(Equal(1))
		Expect(store.RunMigrations(db, store.DialectSQLite)).To(Succeed())
	})

	It("creates every table referenced by the stores", func() {
		Expect(store.RunMigrations(db, store.DialectSQLite)).To(Succeed())

		expected := []string{
			"missions", "mission_tasks", "sessions", "session_messages",
			"turn_costs", "tool_results", "task_outputs", "datasets",
			"dataset_items", "mission_task_subtasks", "task_inputs",
			"mission_events", "route_decisions",
		}
		for _, tbl := range expected {
			var name string
			err := db.QueryRow(
				`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl,
			).Scan(&name)
			Expect(err).NotTo(HaveOccurred(), "missing table: %s", tbl)
			Expect(name).To(Equal(tbl))
		}
	})

	It("rejects unknown dialects", func() {
		err := store.RunMigrations(db, store.Dialect("oracle"))
		Expect(err).To(HaveOccurred())
	})
})

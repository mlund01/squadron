package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// Dialect identifies the SQL flavor a migration targets.
type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrationFilePattern matches files of the form:
//
//	NNNN_<name>.<dialect>.sql
//
// where NNNN is a zero-padded version number and dialect is "sqlite" or
// "postgres". The version, name, and dialect are captured.
var migrationFilePattern = regexp.MustCompile(`^(\d+)_([a-z0-9_]+)\.(sqlite|postgres)\.sql$`)

// Migration is a single schema change at a given version. SQLite and Postgres
// hold dialect-specific DDL loaded from paired .sql files. A migration must
// be idempotent where possible (prefer IF NOT EXISTS) so it is safe to re-run
// if recovery ever interrupts the applied_at insert.
type Migration struct {
	Version  int
	Name     string
	SQLite   string
	Postgres string
}

// LoadMigrations reads the embedded migrations/ directory, pairs up the
// sqlite and postgres variants for each version, and returns them in
// ascending version order.
//
// Invariants enforced:
//   - Every .sql filename must match `NNNN_<name>.<dialect>.sql`.
//   - Each version must have BOTH a .sqlite.sql and .postgres.sql file.
//   - Names must match across dialects for the same version.
//   - Versions must be dense starting at 1 (no gaps).
//
// A violation is a programmer error caught on startup, not silently ignored.
func LoadMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	type pair struct {
		name     string
		sqlite   string
		postgres string
	}
	byVersion := map[int]*pair{}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationFilePattern.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration filename %q does not match NNNN_<name>.<sqlite|postgres>.sql", e.Name())
		}
		version, _ := strconv.Atoi(m[1])
		name := m[2]
		dialect := m[3]

		body, err := fs.ReadFile(migrationFS, "migrations/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}

		p, ok := byVersion[version]
		if !ok {
			p = &pair{name: name}
			byVersion[version] = p
		} else if p.name != name {
			return nil, fmt.Errorf("version %d has mismatched names across dialects: %q vs %q", version, p.name, name)
		}

		switch dialect {
		case "sqlite":
			if p.sqlite != "" {
				return nil, fmt.Errorf("duplicate sqlite migration for version %d", version)
			}
			p.sqlite = string(body)
		case "postgres":
			if p.postgres != "" {
				return nil, fmt.Errorf("duplicate postgres migration for version %d", version)
			}
			p.postgres = string(body)
		}
	}

	versions := make([]int, 0, len(byVersion))
	for v := range byVersion {
		versions = append(versions, v)
	}
	sort.Ints(versions)

	for i, v := range versions {
		if v != i+1 {
			return nil, fmt.Errorf("migration versions must be dense starting at 1; saw gap at %d", v)
		}
		p := byVersion[v]
		if p.sqlite == "" {
			return nil, fmt.Errorf("version %d (%s) missing sqlite file", v, p.name)
		}
		if p.postgres == "" {
			return nil, fmt.Errorf("version %d (%s) missing postgres file", v, p.name)
		}
	}

	out := make([]Migration, 0, len(versions))
	for _, v := range versions {
		p := byVersion[v]
		out = append(out, Migration{
			Version:  v,
			Name:     p.name,
			SQLite:   p.sqlite,
			Postgres: p.postgres,
		})
	}
	return out, nil
}

const migrationsTableSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TEXT NOT NULL
);`

// RunMigrations applies every migration whose Version is greater than the
// highest version already recorded in schema_migrations. Each migration runs
// in its own transaction together with the bookkeeping insert, so a partial
// failure leaves the DB untouched.
func RunMigrations(db *sql.DB, dialect Dialect) error {
	if dialect != DialectSQLite && dialect != DialectPostgres {
		return fmt.Errorf("unknown dialect: %s", dialect)
	}

	if _, err := db.Exec(migrationsTableSQL); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	migrations, err := LoadMigrations()
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}

	applied, err := loadAppliedVersions(db)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}

		sqlText := m.SQLite
		insertStmt := `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`
		if dialect == DialectPostgres {
			sqlText = m.Postgres
			insertStmt = `INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, $3)`
		}

		if err := applyMigration(db, m, sqlText, insertStmt); err != nil {
			return fmt.Errorf("apply migration %d (%s): %w", m.Version, m.Name, err)
		}
	}

	return nil
}

func loadAppliedVersions(db *sql.DB) (map[int]bool, error) {
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter schema_migrations: %w", err)
	}
	return applied, nil
}

func applyMigration(db *sql.DB, m Migration, sqlText, insertStmt string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if sqlText != "" {
		if _, err := tx.Exec(sqlText); err != nil {
			return fmt.Errorf("exec: %w", err)
		}
	}

	if _, err := tx.Exec(insertStmt, m.Version, m.Name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("record version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// MigrationFS exposes the embedded migration files for tests and tooling
// (checksum guardrails, `squadron migrations dump`, etc.). It is read-only.
func MigrationFS() fs.FS {
	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		panic(fmt.Sprintf("store: migrations FS unavailable: %v", err))
	}
	return sub
}

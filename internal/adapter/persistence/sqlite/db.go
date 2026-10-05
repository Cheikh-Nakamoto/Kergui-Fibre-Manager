// Package sqlite is the persistence adapter: a pure-Go SQLite store (no cgo) for
// the device inventory, access rules, routers and encrypted credentials. It
// implements the repository ports declared by the use-case layer.
package sqlite

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, registered as "sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB wraps the database handle and applies migrations on open.
type DB struct {
	sql *sql.DB
}

// Open opens (creating if needed) the database at path and migrates it.
// Use ":memory:" for an ephemeral store (tests).
func Open(path string) (*DB, error) {
	h, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite is a single-writer store; one connection avoids "database is locked".
	h.SetMaxOpenConns(1)
	if err := h.Ping(); err != nil {
		_ = h.Close()
		return nil, err
	}
	db := &DB{sql: h}
	if err := db.migrate(); err != nil {
		_ = h.Close()
		return nil, err
	}
	return db, nil
}

// SQL exposes the underlying handle for repositories in this package.
func (db *DB) SQL() *sql.DB { return db.sql }

// Close closes the database.
func (db *DB) Close() error { return db.sql.Close() }

func (db *DB) migrate() error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := migrationsFS.ReadFile("migrations/" + n)
		if err != nil {
			return err
		}
		if _, err := db.sql.Exec(string(b)); err != nil {
			return fmt.Errorf("apply migration %s: %w", n, err)
		}
	}
	return nil
}

// unixOrZero converts a time to unix seconds, mapping the zero time to 0.
func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// timeOrZero converts unix seconds back to a time, mapping 0 to the zero time.
func timeOrZero(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

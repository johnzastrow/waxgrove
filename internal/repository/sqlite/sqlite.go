// Package sqlite is the SQLite adapter behind the repository interface.
//
// Two things here are load-bearing and easy to get wrong, both from
// docs/requirements.md §7.2:
//
//  1. Two connection pools. database/sql opens multiple connections by default
//     and each gets its own SQLite lock, which manufactures SQLITE_BUSY
//     contention that would not otherwise exist. Writes are forced through a
//     single connection so they serialise cleanly in Go instead.
//
//  2. Pragmas on BOTH pools. WAL gives unlimited concurrent readers alongside
//     one writer; busy_timeout makes a second writer wait rather than fail.
//
// The remaining rule cannot be enforced by this package and must be respected
// by callers: never hold a write transaction across network I/O. Resolve
// against MusicBrainz or a provider first, then open a short transaction.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver; keeps CGO_ENABLED=0 (§7)
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store holds the split read/write pools described in §7.2.
type Store struct {
	// write has exactly one connection so concurrent writers queue in Go
	// rather than colliding on SQLite's lock.
	write *sql.DB
	// read may open many connections; WAL readers do not block each other
	// or the writer.
	read *sql.DB
	path string
}

// Open prepares both pools, applies the required pragmas and runs migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	w, err := openPool(path, 1)
	if err != nil {
		return nil, fmt.Errorf("open write pool: %w", err)
	}
	r, err := openPool(path, 8)
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("open read pool: %w", err)
	}

	s := &Store{write: w, read: r, path: path}

	if err := s.verifyPragmas(ctx); err != nil {
		_ = s.Close()
		return nil, err
	}
	if err := s.Migrate(ctx); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// requiredPragmas are applied via the DSN so every connection in a pool gets
// them, including ones opened later by database/sql.
var requiredPragmas = []string{
	"journal_mode(WAL)",   // concurrent readers + one writer
	"busy_timeout(5000)",  // wait rather than returning SQLITE_BUSY
	"foreign_keys(ON)",    // the schema leans on ON DELETE SET NULL for GDPR
	"synchronous(NORMAL)", // safe under WAL
}

func openPool(path string, maxOpen int) (*sql.DB, error) {
	dsn := path + "?" + pragmaQuery()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxOpen)
	db.SetConnMaxLifetime(time.Hour)
	return db, nil
}

func pragmaQuery() string {
	v := url.Values{}
	for _, p := range requiredPragmas {
		v.Add("_pragma", p)
	}
	return v.Encode()
}

// verifyPragmas asserts the settings actually took effect. A silently ignored
// DSN parameter would leave the database without WAL, which changes the
// concurrency story entirely — better to fail at startup than to discover it
// under load.
func (s *Store) verifyPragmas(ctx context.Context) error {
	for _, pool := range []*sql.DB{s.write, s.read} {
		var journal string
		if err := pool.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
			return fmt.Errorf("read journal_mode: %w", err)
		}
		if !strings.EqualFold(journal, "wal") {
			return fmt.Errorf("journal_mode is %q, want wal (§7.2)", journal)
		}
		var fk int
		if err := pool.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			return fmt.Errorf("read foreign_keys: %w", err)
		}
		if fk != 1 {
			return fmt.Errorf("foreign_keys is off, want on (§7.2)")
		}
	}
	return nil
}

// Migrate applies every embedded migration that has not yet run, in order,
// each inside its own transaction.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.write.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return err
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // 0001_, 0002_, … applied in lexical order

	for _, name := range names {
		var seen int
		if err := s.write.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name).Scan(&seen); err != nil {
			return err
		}
		if seen > 0 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if err := s.applyMigration(ctx, name, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, name, body string) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
		name, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

// Reader returns the multi-connection pool. Use for every SELECT.
func (s *Store) Reader() *sql.DB { return s.read }

// Writer returns the single-connection pool. Use for every mutation, and keep
// transactions short — never span network I/O (§7.2).
func (s *Store) Writer() *sql.DB { return s.write }

// Ping checks both pools.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.read.PingContext(ctx); err != nil {
		return fmt.Errorf("read pool: %w", err)
	}
	if err := s.write.PingContext(ctx); err != nil {
		return fmt.Errorf("write pool: %w", err)
	}
	return nil
}

// Close shuts both pools down.
func (s *Store) Close() error {
	var first error
	if err := s.read.Close(); err != nil {
		first = err
	}
	if err := s.write.Close(); err != nil && first == nil {
		first = err
	}
	return first
}

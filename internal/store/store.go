// Package store owns the control plane's SQLite database: connection, migrations and transactions.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/thomas-bsn/forgeyard/internal/store/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store wraps the database and its generated queries.
type Store struct {
	DB *sql.DB
	*db.Queries
}

// Open opens (or creates) the SQLite database at path and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	params := url.Values{}
	params.Add("_pragma", "foreign_keys(1)")
	params.Add("_pragma", "journal_mode(WAL)")
	params.Add("_pragma", "busy_timeout(5000)")
	conn, err := sql.Open("sqlite", "file:"+path+"?"+params.Encode())
	if err != nil {
		return nil, err
	}
	// SQLite allows a single writer; one connection avoids "database is locked" errors.
	conn.SetMaxOpenConns(1)

	if err := migrate(ctx, conn); err != nil {
		conn.Close()
		return nil, err
	}
	return &Store{DB: conn, Queries: db.New(conn)}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.DB.Close()
}

// InTx runs fn in a transaction, committed if fn returns nil and rolled back otherwise.
func (s *Store) InTx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(s.Queries.WithTx(tx)); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// migrate applies, in filename order, every embedded migration not yet recorded in schema_migrations.
func migrate(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	for _, path := range names {
		name := strings.TrimPrefix(path, "migrations/")
		var applied int
		if err := conn.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name).Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}

		script, err := migrations.ReadFile(path)
		if err != nil {
			return err
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(script)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Package store contains SQLite persistence for UI state and mirror health.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS ui_state (
    user_id INTEGER PRIMARY KEY,
    chat_id INTEGER,
    message_id INTEGER,
    screen TEXT,
    query TEXT,
    page INTEGER DEFAULT 0,
    book_id INTEGER,
    operation TEXT,
    extra TEXT,
    updated_at REAL DEFAULT (strftime('%s','now'))
);

CREATE TABLE IF NOT EXISTS mirror_stats (
    url TEXT PRIMARY KEY,
    status TEXT,
    failures INTEGER DEFAULT 0,
    last_check REAL,
    last_error TEXT
);
`

// Database wraps a single SQLite connection.
type Database struct {
	path string
	db   *sql.DB
}

// Open creates or opens the database, applies the schema and sets private
// permissions on the database file.
func Open(path string) (*Database, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// A single connection keeps writes serialized and avoids SQLITE_BUSY
	// surprises on small devices.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx := context.Background()
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA foreign_keys=ON;",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("apply %s: %w", pragma, err)
		}
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Chmod(path+suffix, 0o600); err != nil && !os.IsNotExist(err) {
			db.Close()
			return nil, fmt.Errorf("chmod database%s: %w", suffix, err)
		}
	}
	return &Database{path: path, db: db}, nil
}

// Close closes the database.
func (d *Database) Close() error {
	if d.db == nil {
		return nil
	}
	err := d.db.Close()
	d.db = nil
	return err
}

// DB exposes the raw handle for stores that live in other packages.
func (d *Database) DB() *sql.DB {
	return d.db
}

// Exec runs a statement and returns the result.
func (d *Database) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.db.ExecContext(ctx, query, args...)
}

// QueryRow runs a query returning at most one row.
func (d *Database) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return d.db.QueryRowContext(ctx, query, args...)
}

// Query runs a query returning rows.
func (d *Database) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.db.QueryContext(ctx, query, args...)
}

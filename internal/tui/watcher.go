package tui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
)

// DataVersionWatcher compares PRAGMA data_version on one pinned SQLite
// connection. Values from pooled or replacement connections are unrelated
// and therefore useless for cross-process change detection.
type DataVersionWatcher struct {
	db   *sql.DB
	conn *sql.Conn
}

// OpenDataVersionWatcher opens a dedicated, otherwise-idle connection to the
// board database. It never holds a transaction across polls.
func OpenDataVersionWatcher(ctx context.Context, path string) (*DataVersionWatcher, error) {
	dsn := (&url.URL{Scheme: "file", Path: path}).String() + "?_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("tui: open data-version database: %w", err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("tui: pin data-version connection: %w", err)
	}
	return &DataVersionWatcher{db: db, conn: conn}, nil
}

// DataVersion reads SQLite's connection-local external-change counter.
func (w *DataVersionWatcher) DataVersion(ctx context.Context) (int64, error) {
	var version int64
	if err := w.conn.QueryRowContext(ctx, "PRAGMA data_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("tui: read data_version: %w", err)
	}
	return version, nil
}

// Close releases the pinned connection and its one-connection pool.
func (w *DataVersionWatcher) Close() error {
	return errors.Join(w.conn.Close(), w.db.Close())
}

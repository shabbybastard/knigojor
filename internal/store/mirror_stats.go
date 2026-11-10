package store

import (
	"context"

	"github.com/shabbybastard/knigojor/internal/netx"
)

// MirrorStatsStore persists mirror health rows.
type MirrorStatsStore struct {
	db *Database
}

// NewMirrorStatsStore creates a mirror stats store.
func NewMirrorStatsStore(db *Database) *MirrorStatsStore {
	return &MirrorStatsStore{db: db}
}

// Save upserts one mirror row.
func (s *MirrorStatsStore) Save(ctx context.Context, stat netx.PersistedMirror) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO mirror_stats (url, status, failures, last_check, last_error)
		 VALUES (?,?,?,?,?)
		 ON CONFLICT(url) DO UPDATE SET
		   status=excluded.status,
		   failures=excluded.failures,
		   last_check=excluded.last_check,
		   last_error=excluded.last_error`,
		stat.URL, stat.Status, stat.Failures, stat.LastCheck, stat.LastError,
	)
	return err
}

// Load returns all persisted mirror rows.
func (s *MirrorStatsStore) Load(ctx context.Context) ([]netx.PersistedMirror, error) {
	rows, err := s.db.Query(ctx,
		`SELECT url, status, failures, last_check, last_error FROM mirror_stats`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]netx.PersistedMirror, 0)
	for rows.Next() {
		var (
			stat      netx.PersistedMirror
			status    *string
			lastCheck *float64
			lastError *string
		)
		if err := rows.Scan(&stat.URL, &status, &stat.Failures, &lastCheck, &lastError); err != nil {
			return nil, err
		}
		if status != nil {
			stat.Status = *status
		}
		if lastCheck != nil {
			stat.LastCheck = *lastCheck
		}
		if lastError != nil {
			stat.LastError = *lastError
		}
		out = append(out, stat)
	}
	return out, rows.Err()
}

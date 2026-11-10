package netx

import "context"

// PersistedMirror is the durable part of a mirror's health state.
type PersistedMirror struct {
	URL       string
	Status    string
	Failures  int
	LastCheck float64
	LastError string
}

// StatsStore persists mirror health between restarts.
type StatsStore interface {
	Save(ctx context.Context, stat PersistedMirror) error
	Load(ctx context.Context) ([]PersistedMirror, error)
}

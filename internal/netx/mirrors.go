package netx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// MirrorState is the circuit-breaker state of a mirror.
type MirrorState string

const (
	MirrorHealthy     MirrorState = "healthy"
	MirrorDegraded    MirrorState = "degraded"
	MirrorUnavailable MirrorState = "unavailable"
	MirrorHalfOpen    MirrorState = "half_open"
)

const mirrorCooldown = 60 * time.Second
const failureThreshold = 3

// NonMirrorError marks an operation error that should not penalize a mirror
// (for example, a downloaded EPUB that failed validation).
type NonMirrorError struct {
	Err error
}

func (e *NonMirrorError) Error() string { return e.Err.Error() }
func (e *NonMirrorError) Unwrap() error { return e.Err }

// Mirror is one Flibusta endpoint with a circuit breaker.
type Mirror struct {
	mu        sync.Mutex
	url       string
	state     MirrorState
	failures  int
	lastCheck time.Time
	lastError string
	isOnion   bool
}

// NewMirror creates a healthy mirror.
func NewMirror(rawURL string, isOnion bool) *Mirror {
	return &Mirror{url: rawURL, state: MirrorHealthy, isOnion: isOnion}
}

func (m *Mirror) URL() string   { return m.url }
func (m *Mirror) IsOnion() bool { return m.isOnion }

// LastError returns the last failure reason under the mirror lock.
func (m *Mirror) LastError() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastError
}

// MarkSuccess resets the breaker.
func (m *Mirror) MarkSuccess() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = MirrorHealthy
	m.failures = 0
	m.lastCheck = time.Now()
}

// MarkFailure increments failures and opens the breaker after the threshold.
func (m *Mirror) MarkFailure(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failures++
	m.lastError = reason
	m.lastCheck = time.Now()
	if m.failures >= failureThreshold && m.state != MirrorUnavailable {
		m.state = MirrorUnavailable
		slog.Warn("mirror marked unavailable", "url", m.url, "failures", m.failures)
	}
}

// IsAvailable applies the half-open cooldown.
func (m *Mirror) IsAvailable() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch m.state {
	case MirrorHealthy, MirrorDegraded, MirrorHalfOpen:
		return true
	case MirrorUnavailable:
		if time.Since(m.lastCheck) > mirrorCooldown {
			m.state = MirrorHalfOpen
			return true
		}
	}
	return false
}

// State returns a copy of the breaker state.
func (m *Mirror) State() MirrorState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Failures returns the current failure count.
func (m *Mirror) Failures() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failures
}

// Snapshot returns the durable state.
func (m *Mirror) Snapshot() PersistedMirror {
	m.mu.Lock()
	defer m.mu.Unlock()
	return PersistedMirror{
		URL:       m.url,
		Status:    string(m.state),
		Failures:  m.failures,
		LastCheck: float64(m.lastCheck.Unix()),
		LastError: m.lastError,
	}
}

// Apply restores persistent state.
func (m *Mirror) Apply(stat PersistedMirror) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch MirrorState(stat.Status) {
	case MirrorHealthy, MirrorDegraded, MirrorUnavailable, MirrorHalfOpen:
		m.state = MirrorState(stat.Status)
	}
	m.failures = stat.Failures
	m.lastError = stat.LastError
	if stat.LastCheck > 0 {
		m.lastCheck = time.Unix(int64(stat.LastCheck), 0)
	}
}

// MirrorManager owns direct and onion mirrors.
type MirrorManager struct {
	mu           sync.RWMutex
	mirrors      []*Mirror
	onion        []*Mirror
	torEnabled   bool
	torAvailable bool
	stats        StatsStore
}

// NewMirrorManager builds a manager from configured URLs.
func NewMirrorManager(mirrors, onion []string, torEnabled bool, stats StatsStore) *MirrorManager {
	mm := &MirrorManager{
		mirrors:      make([]*Mirror, 0, len(mirrors)),
		onion:        make([]*Mirror, 0, len(onion)),
		torEnabled:   torEnabled,
		torAvailable: torEnabled,
		stats:        stats,
	}
	for _, item := range mirrors {
		mm.mirrors = append(mm.mirrors, NewMirror(item, false))
	}
	for _, item := range onion {
		mm.onion = append(mm.onion, NewMirror(item, true))
	}
	return mm
}

// SetTorAvailable updates whether onion mirrors may be used.
func (mm *MirrorManager) SetTorAvailable(value bool) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.torAvailable = value
}

// LoadStats restores persisted mirror states.
func (mm *MirrorManager) LoadStats(ctx context.Context) {
	if mm.stats == nil {
		return
	}
	rows, err := mm.stats.Load(ctx)
	if err != nil {
		slog.Warn("failed to load mirror stats", "error", err)
		return
	}
	byURL := make(map[string]PersistedMirror, len(rows))
	for _, row := range rows {
		byURL[row.URL] = row
	}
	for _, mirror := range mm.AllMirrors() {
		if row, ok := byURL[mirror.URL()]; ok {
			mirror.Apply(row)
		}
	}
}

func (mm *MirrorManager) persist(ctx context.Context, mirror *Mirror) {
	if mm.stats == nil {
		return
	}
	if err := mm.stats.Save(ctx, mirror.Snapshot()); err != nil {
		slog.Warn("failed to persist mirror stats", "url", mirror.URL(), "error", err)
	}
}

// AllMirrors returns direct mirrors plus onion mirrors when Tor is usable.
func (mm *MirrorManager) AllMirrors() []*Mirror {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	out := make([]*Mirror, 0, len(mm.mirrors)+len(mm.onion))
	out = append(out, mm.mirrors...)
	if mm.torEnabled && mm.torAvailable {
		out = append(out, mm.onion...)
	}
	return out
}

// AllSources returns mirrors ordered by availability and failure count.
func (mm *MirrorManager) AllSources(includeOnion bool) []*Mirror {
	mm.mu.RLock()
	source := make([]*Mirror, 0, len(mm.mirrors)+len(mm.onion))
	source = append(source, mm.mirrors...)
	if includeOnion && mm.torEnabled && mm.torAvailable {
		source = append(source, mm.onion...)
	}
	mm.mu.RUnlock()

	sort.SliceStable(source, func(i, j int) bool {
		ai, aj := source[i].IsAvailable(), source[j].IsAvailable()
		if ai != aj {
			return ai
		}
		return source[i].Failures() < source[j].Failures()
	})
	return source
}

// RunWithFailover tries mirrors in order and returns the first success.
func RunWithFailover[T any](
	ctx context.Context,
	mm *MirrorManager,
	op func(context.Context, *Mirror) (T, error),
) (T, *Mirror, error) {
	var zero T
	var lastErr error
	for _, mirror := range mm.AllSources(true) {
		if !mirror.IsAvailable() {
			continue
		}
		result, err := op(ctx, mirror)
		if err == nil {
			mirror.MarkSuccess()
			mm.persist(ctx, mirror)
			return result, mirror, nil
		}
		lastErr = err
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return zero, nil, err
		}
		var nonMirror *NonMirrorError
		if errors.As(err, &nonMirror) {
			slog.Info("mirror content rejected", "url", mirror.URL(), "error", err)
			continue
		}
		mirror.MarkFailure(err.Error())
		mm.persist(ctx, mirror)
		slog.Info("mirror failed", "url", mirror.URL(), "error", err)
	}
	if lastErr != nil {
		return zero, nil, lastErr
	}
	return zero, nil, fmt.Errorf("no mirrors available")
}

// StatusRow is the public status of one mirror.
type StatusRow struct {
	URL       string
	State     string
	Failures  int
	IsOnion   bool
	LastError string
}

// Status returns all configured mirrors for the status screen.
func (mm *MirrorManager) Status() []StatusRow {
	out := make([]StatusRow, 0)
	for _, mirror := range mm.AllMirrors() {
		out = append(out, StatusRow{
			URL:       mirror.URL(),
			State:     string(mirror.State()),
			Failures:  mirror.Failures(),
			IsOnion:   mirror.IsOnion(),
			LastError: mirror.LastError(),
		})
	}
	return out
}

// HasAvailable reports whether at least one mirror is usable.
func (mm *MirrorManager) HasAvailable() bool {
	for _, mirror := range mm.AllMirrors() {
		if mirror.IsAvailable() {
			return true
		}
	}
	return false
}

// TorConfigured reports whether onion support is enabled.
func (mm *MirrorManager) TorConfigured() bool {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return mm.torEnabled
}

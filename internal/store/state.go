// Package store keeps the single-message interface state in memory.
package store

import (
	"context"
	"sync"
)

// UIState is the persisted single-message interface state for one user.
type UIState struct {
	UserID    int64
	ChatID    int64
	MessageID int
	Screen    string
	Query     string
	Page      int
	BookID    int64
	Operation string
	Extra     map[string]any
}

// NewUIState returns a fresh main-screen state.
func NewUIState(userID int64) *UIState {
	return &UIState{UserID: userID, Screen: "main", Extra: map[string]any{}}
}

// StateStore keeps UIState in memory. State does not survive a restart.
type StateStore struct {
	mu     sync.RWMutex
	states map[int64]*UIState
}

// NewStateStore creates an in-memory state store.
func NewStateStore() *StateStore {
	return &StateStore{states: make(map[int64]*UIState)}
}

// Get loads a copy of the state for a user, returning a fresh state when none
// exists.
func (s *StateStore) Get(ctx context.Context, userID int64) (*UIState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	state, ok := s.states[userID]
	s.mu.RUnlock()
	if !ok {
		return NewUIState(userID), nil
	}
	return cloneUIState(state), nil
}

// Save stores a copy of the state.
func (s *StateStore) Save(ctx context.Context, state *UIState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if state == nil {
		return nil
	}
	s.mu.Lock()
	s.states[state.UserID] = cloneUIState(state)
	s.mu.Unlock()
	return nil
}

// Delete removes a user's state.
func (s *StateStore) Delete(ctx context.Context, userID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.states, userID)
	s.mu.Unlock()
	return nil
}

func cloneUIState(state *UIState) *UIState {
	if state == nil {
		return nil
	}
	clone := *state
	clone.Extra = make(map[string]any, len(state.Extra))
	for key, value := range state.Extra {
		clone.Extra[key] = value
	}
	return &clone
}

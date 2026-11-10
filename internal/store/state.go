package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

// StateStore persists UIState.
type StateStore struct {
	db *Database
}

// NewStateStore creates a state store.
func NewStateStore(db *Database) *StateStore {
	return &StateStore{db: db}
}

// Get loads state for a user, returning a fresh state when none exists.
func (s *StateStore) Get(ctx context.Context, userID int64) (*UIState, error) {
	row := s.db.QueryRow(ctx,
		`SELECT user_id, chat_id, message_id, screen, query, page, book_id, operation, extra
		   FROM ui_state WHERE user_id=?`, userID)

	var (
		state     UIState
		chatID    sql.NullInt64
		messageID sql.NullInt64
		screen    sql.NullString
		query     sql.NullString
		page      sql.NullInt64
		bookID    sql.NullInt64
		operation sql.NullString
		extra     sql.NullString
	)
	err := row.Scan(
		&state.UserID, &chatID, &messageID, &screen, &query, &page,
		&bookID, &operation, &extra,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return NewUIState(userID), nil
	}
	if err != nil {
		return nil, err
	}
	state.ChatID = chatID.Int64
	state.MessageID = int(messageID.Int64)
	state.Screen = screen.String
	if state.Screen == "" {
		state.Screen = "main"
	}
	state.Query = query.String
	state.Page = int(page.Int64)
	state.BookID = bookID.Int64
	state.Operation = operation.String
	state.Extra = map[string]any{}
	if extra.String != "" {
		_ = json.Unmarshal([]byte(extra.String), &state.Extra)
	}
	return &state, nil
}

// Save upserts state.
func (s *StateStore) Save(ctx context.Context, state *UIState) error {
	if state.Extra == nil {
		state.Extra = map[string]any{}
	}
	extra, err := json.Marshal(state.Extra)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`INSERT INTO ui_state
		   (user_id, chat_id, message_id, screen, query, page, book_id, operation, extra, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?, strftime('%s','now'))
		 ON CONFLICT(user_id) DO UPDATE SET
		   chat_id=excluded.chat_id,
		   message_id=excluded.message_id,
		   screen=excluded.screen,
		   query=excluded.query,
		   page=excluded.page,
		   book_id=excluded.book_id,
		   operation=excluded.operation,
		   extra=excluded.extra,
		   updated_at=excluded.updated_at`,
		state.UserID,
		nullInt64(state.ChatID),
		nullInt(state.MessageID),
		state.Screen,
		nullString(state.Query),
		state.Page,
		nullInt64(state.BookID),
		nullString(state.Operation),
		string(extra),
	)
	return err
}

// Delete removes a user's state.
func (s *StateStore) Delete(ctx context.Context, userID int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM ui_state WHERE user_id=?`, userID)
	return err
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

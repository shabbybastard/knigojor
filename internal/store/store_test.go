package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/shabbybastard/knigojor/internal/netx"
)

func openTestDB(t *testing.T) *Database {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestStateRoundTrip(t *testing.T) {
	db := openTestDB(t)
	states := NewStateStore(db)
	ctx := context.Background()

	state := NewUIState(7)
	state.ChatID = 10
	state.MessageID = 55
	state.Screen = "results"
	state.Query = "Пушкин"
	state.Page = 3
	state.BookID = 42
	state.Operation = "download"
	state.Extra = map[string]any{"total": 562, "total_pages": 113}
	if err := states.Save(ctx, state); err != nil {
		t.Fatal(err)
	}

	loaded, err := states.Get(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Screen != "results" || loaded.Query != "Пушкин" || loaded.Page != 3 {
		t.Fatalf("unexpected state: %#v", loaded)
	}
	if loaded.ChatID != 10 || loaded.MessageID != 55 || loaded.BookID != 42 {
		t.Fatalf("unexpected ids: %#v", loaded)
	}
	if loaded.Extra["total"].(float64) != 562 {
		t.Fatalf("unexpected extra: %#v", loaded.Extra)
	}

	if err := states.Delete(ctx, 7); err != nil {
		t.Fatal(err)
	}
	loaded, err = states.Get(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Screen != "main" || loaded.ChatID != 0 {
		t.Fatalf("delete did not reset state: %#v", loaded)
	}
}

func TestMirrorStatsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	stats := NewMirrorStatsStore(db)
	ctx := context.Background()

	row := netx.PersistedMirror{
		URL:       "https://mirror",
		Status:    "degraded",
		Failures:  2,
		LastCheck: 123.5,
		LastError: "timeout",
	}
	if err := stats.Save(ctx, row); err != nil {
		t.Fatal(err)
	}
	rows, err := stats.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0] != row {
		t.Fatalf("unexpected rows: %#v", rows)
	}
}

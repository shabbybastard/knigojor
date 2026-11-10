package store

import (
	"context"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	states := NewStateStore()
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
	// Mutating the caller's copy must not leak into the stored state.
	state.Query = "mutated"
	state.Extra["total"] = 0

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
	if loaded.Extra["total"].(int) != 562 {
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

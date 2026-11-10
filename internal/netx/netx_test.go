package netx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, retries int) *Client {
	t.Helper()
	client, err := NewClient(5*time.Second, 2, "", retries)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func TestGetTextRetries5xx(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "temporary", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := testClient(t, 1)
	body, err := client.GetText(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if body != "ok" {
		t.Fatalf("unexpected body %q", body)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got %d", calls.Load())
	}
}

func TestGetTextDoesNotRetry4xx(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer server.Close()

	client := testClient(t, 3)
	_, err := client.GetText(context.Background(), server.URL)
	if err == nil {
		t.Fatal("expected error")
	}
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.Code != http.StatusNotFound {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", calls.Load())
	}
}

func TestDownloadEnforcesSizeLimit(t *testing.T) {
	payload := make([]byte, 2*1024*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	client := testClient(t, 0)
	dest := filepath.Join(t.TempDir(), "book.epub")
	_, err := client.Download(context.Background(), server.URL, dest, 1024*1024, 5*time.Second)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("expected ErrFileTooLarge, got %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatal("partial file was not removed")
	}
}

func TestMirrorFailover(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer good.Close()

	manager := NewMirrorManager([]string{bad.URL, good.URL}, nil, false, nil)
	client := testClient(t, 0)
	result, mirror, err := RunWithFailover(context.Background(), manager, func(ctx context.Context, mirror *Mirror) (string, error) {
		return client.GetText(ctx, mirror.URL())
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" || mirror.URL() != good.URL {
		t.Fatalf("unexpected result: %q %s", result, mirror.URL())
	}
	if manager.mirrors[0].Failures() != 1 {
		t.Fatalf("bad mirror was not penalized: %d", manager.mirrors[0].Failures())
	}
	if manager.mirrors[1].Failures() != 0 {
		t.Fatalf("good mirror was penalized: %d", manager.mirrors[1].Failures())
	}
}

func TestNonMirrorErrorDoesNotPenalize(t *testing.T) {
	manager := NewMirrorManager([]string{"https://one"}, nil, false, nil)
	_, _, err := RunWithFailover(context.Background(), manager, func(_ context.Context, _ *Mirror) (string, error) {
		return "", &NonMirrorError{Err: errors.New("invalid content")}
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if manager.mirrors[0].Failures() != 0 {
		t.Fatalf("mirror was penalized: %d", manager.mirrors[0].Failures())
	}
}

func TestMirrorAccessorsAreRaceFree(t *testing.T) {
	mirror := NewMirror("https://mirror", false)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			mirror.MarkFailure("boom")
		}
	}()
	for i := 0; i < 1000; i++ {
		_ = mirror.LastError()
		_ = mirror.State()
		_ = mirror.Failures()
	}
	<-done
}

func TestCheckMirror(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	client := testClient(t, 0)
	if !CheckMirror(context.Background(), client, server.URL) {
		t.Fatal("healthy server marked down")
	}
	if CheckMirror(context.Background(), client, "http://127.0.0.1:1") {
		t.Fatal("closed port marked up")
	}
}

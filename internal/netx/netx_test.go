package netx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, retries int) *Client {
	t.Helper()
	client, err := NewClient(5*time.Second, 2, retries)
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
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
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

func TestDownloadReportsProgress(t *testing.T) {
	payload := make([]byte, 256*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	client := testClient(t, 0)
	dest := filepath.Join(t.TempDir(), "book.epub")
	var written, total int64
	calls := 0
	_, err := client.DownloadOnceProgress(
		context.Background(), server.URL, dest, 2*1024*1024, 5*time.Second,
		func(current, size int64) {
			calls++
			written = current
			total = size
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 || written != int64(len(payload)) || total != int64(len(payload)) {
		t.Fatalf("unexpected progress: calls=%d written=%d total=%d", calls, written, total)
	}
}

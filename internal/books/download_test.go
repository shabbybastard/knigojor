package books

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shabbybastard/knigojor/internal/netx"
)

func zipBytes(t *testing.T, name string, data []byte, deflate bool) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: name}
	if deflate {
		header.Method = zip.Deflate
	}
	part, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func testHTTP(t *testing.T) *netx.Client {
	t.Helper()
	client, err := netx.NewClient(5*time.Second, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func TestDownloadFB2ExtractsNestedFile(t *testing.T) {
	archive := zipBytes(t, "dir/book.fb2", []byte("<FictionBook/>"), false)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	dir := t.TempDir()
	result, err := DownloadFB2Zip(context.Background(), testHTTP(t), server.URL, dir, 1, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result != filepath.Join(dir, "dir", "book.fb2") {
		t.Fatalf("unexpected path: %s", result)
	}
	if _, err := os.Stat(result); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "book.fb2.zip")); !os.IsNotExist(err) {
		t.Fatal("temporary archive was not removed")
	}
}

func TestDownloadFB2RejectsZipBomb(t *testing.T) {
	archive := zipBytes(t, "book.fb2", bytes.Repeat([]byte("0"), 2*1024*1024), true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	_, err := DownloadFB2Zip(context.Background(), testHTTP(t), server.URL, t.TempDir(), 1, 5*time.Second)
	if err == nil {
		t.Fatal("expected zip bomb error")
	}
}

func TestDownloadEPUBRejectsEmptyFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "book.epub")
	_, err := DownloadEPUB(context.Background(), testHTTP(t), server.URL, dest, 1, 5*time.Second)
	if err == nil {
		t.Fatal("expected empty file error")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatal("empty file was not removed")
	}
}

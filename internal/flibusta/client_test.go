package flibusta

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shabbybastard/knigojor/internal/netx"
)

func TestClientSearchAndBookWithFailover(t *testing.T) {
	searchHTML, err := os.ReadFile(filepath.Join("..", "..", "testdata", "search_pushkin.html"))
	if err != nil {
		t.Fatal(err)
	}
	bookHTML, err := os.ReadFile(filepath.Join("..", "..", "testdata", "book_820555.html"))
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/booksearch":
			_, _ = w.Write(searchHTML)
		case r.URL.Path == "/b/820555":
			_, _ = w.Write(bookHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	httpClient, err := netx.NewClient(5*time.Second, 2, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer httpClient.Close()

	mirrors := netx.NewMirrorManager([]string{"http://127.0.0.1:1", server.URL}, nil, false, nil)
	client := NewClient(mirrors, httpClient, nil)

	results, err := client.Search(context.Background(), "Пушкин", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Books) != 50 || results.Total != 562 {
		t.Fatalf("unexpected search results: %#v", results)
	}

	info, err := client.GetBook(context.Background(), 820555)
	if err != nil {
		t.Fatal(err)
	}
	if info.Title == "" || len(info.Authors) == 0 {
		t.Fatalf("unexpected book: %#v", info)
	}
	if mirrors.Status()[0].Failures == 0 {
		t.Fatal("failed mirror was not penalized")
	}
}

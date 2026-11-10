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

func TestClientSearchBookAndAuthor(t *testing.T) {
	searchHTML, err := os.ReadFile(filepath.Join("..", "..", "testdata", "search_pushkin.html"))
	if err != nil {
		t.Fatal(err)
	}
	bookHTML, err := os.ReadFile(filepath.Join("..", "..", "testdata", "book_820555.html"))
	if err != nil {
		t.Fatal(err)
	}
	authorHTML, err := os.ReadFile(filepath.Join("..", "..", "testdata", "author_sample.html"))
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/booksearch":
			if r.URL.Query().Get("cha") != "1" ||
				r.URL.Query().Get("chb") != "1" ||
				r.URL.Query().Get("chs") != "1" {
				t.Errorf("author/book flags are missing: %s", r.URL.RawQuery)
			}
			_, _ = w.Write(searchHTML)
		case r.URL.Path == "/b/820555":
			_, _ = w.Write(bookHTML)
		case r.URL.Path == "/a/7171":
			if r.URL.Query().Get("order") != "a" {
				t.Errorf("author page is not sorted alphabetically: %s", r.URL.RawQuery)
			}
			_, _ = w.Write(authorHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	httpClient, err := netx.NewClient(5*time.Second, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer httpClient.Close()

	client := NewClient(server.URL, httpClient)

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
	if info.SourceURL != server.URL+"/b/820555" {
		t.Fatalf("unexpected source URL: %s", info.SourceURL)
	}

	author, err := client.GetAuthorBooks(context.Background(), 7171)
	if err != nil {
		t.Fatal(err)
	}
	if author.Name == "" || len(author.Books) != 12 {
		t.Fatalf("unexpected author books: %#v", author)
	}
}

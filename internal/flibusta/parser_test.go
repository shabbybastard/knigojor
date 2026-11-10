package flibusta

import (
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestParseBook(t *testing.T) {
	info, err := ParseBook(fixture(t, "book_820555.html"), 820555, "https://mirror.example")
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "Война и мир" && info.Title != "Война и мир (fb2)" {
		t.Fatalf("unexpected title %q", info.Title)
	}
	if len(info.Authors) != 1 || info.Authors[0] != "Лев Николаевич Толстой" {
		t.Fatalf("unexpected authors %#v", info.Authors)
	}
	if !contains(info.Genres, "Классическая проза ХIX века") {
		t.Fatalf("missing genre: %#v", info.Genres)
	}
	if info.Size != "14146K" || info.Pages == nil || *info.Pages != 1545 {
		t.Fatalf("unexpected size/pages: %q %v", info.Size, info.Pages)
	}
	if info.BookRatingCount == nil || *info.BookRatingCount != 4 {
		t.Fatalf("unexpected rating count: %v", info.BookRatingCount)
	}
	if info.BookRatingAvg == nil || *info.BookRatingAvg < 3.79 || *info.BookRatingAvg > 3.81 {
		t.Fatalf("unexpected rating avg: %v", info.BookRatingAvg)
	}
	if !info.HasFB2 || !info.HasEPUB {
		t.Fatalf("unexpected formats: %#v", info.Formats)
	}
	if info.Annotation == "" || !containsText(info.Annotation, "В минуты отъезда") {
		t.Fatalf("unexpected annotation: %q", info.Annotation)
	}
	if !containsText(info.Annotation, "книге. В") && containsText(info.Annotation, "книге.В") {
		t.Fatalf("annotation words were glued: %q", info.Annotation)
	}
	if info.CoverURL == "" || info.SourceURL != "https://mirror.example/b/820555" {
		t.Fatalf("unexpected cover/source: %q %q", info.CoverURL, info.SourceURL)
	}
}

func TestParseSearch(t *testing.T) {
	result, err := ParseSearch(fixture(t, "search_pushkin.html"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Books) != 50 || result.Total != 562 {
		t.Fatalf("unexpected result count: %d / %d", len(result.Books), result.Total)
	}
	if !result.HasNext || result.HasPrev {
		t.Fatalf("unexpected pagination: next=%v prev=%v", result.HasNext, result.HasPrev)
	}
	first := result.Books[0]
	if first.ID != 552916 || first.Title == "" {
		t.Fatalf("unexpected first book: %#v", first)
	}
	if len(first.Authors) != 1 || first.Authors[0] != "Борис Степанович Житков" {
		t.Fatalf("unexpected first authors: %#v", first.Authors)
	}
}

func TestParseSearchAuthors(t *testing.T) {
	result, err := ParseSearch(fixture(t, "search_authors.html"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Authors) != 2 {
		t.Fatalf("unexpected authors: %#v", result.Authors)
	}
	first := result.Authors[0]
	if first.ID != 7171 || first.Name != "Лев Николаевич Толстой" {
		t.Fatalf("unexpected first author: %#v", first)
	}
	if !containsText(first.Details, "879 книг") {
		t.Fatalf("unexpected author details: %q", first.Details)
	}
	if len(result.Books) != 2 || result.Total != 2 {
		t.Fatalf("unexpected books: %#v", result.Books)
	}
}

func TestParseAuthorBooks(t *testing.T) {
	result, err := ParseAuthorBooks(fixture(t, "author_sample.html"), 7171)
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "Лев Николаевич Толстой" {
		t.Fatalf("unexpected author name: %q", result.Name)
	}
	if len(result.Books) != 12 {
		t.Fatalf("unexpected book count: %d", len(result.Books))
	}
	if result.Books[0].ID != 101 || result.Books[0].Title != "Анна Каренина" {
		t.Fatalf("unexpected first book: %#v", result.Books[0])
	}
	if result.Books[11].ID != 112 {
		t.Fatalf("unexpected last book: %#v", result.Books[11])
	}
}

func TestParseAuthorBooksRejectsWrongPage(t *testing.T) {
	if _, err := ParseAuthorBooks("<html><body><h1>Ошибка</h1></body></html>", 7171); err == nil {
		t.Fatal("wrong page was accepted as an author page")
	}
}

func containsText(value, wanted string) bool {
	return len(value) >= len(wanted) && (value == wanted ||
		len(wanted) == 0 || indexOf(value, wanted) >= 0)
}

func indexOf(value, wanted string) int {
	for i := 0; i+len(wanted) <= len(value); i++ {
		if value[i:i+len(wanted)] == wanted {
			return i
		}
	}
	return -1
}

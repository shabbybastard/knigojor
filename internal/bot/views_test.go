package bot

import (
	"context"
	"strings"
	"testing"

	"github.com/shabbybastard/knigojor/internal/flibusta"
)

type fakeSearcher struct {
	calls []int
}

func (f *fakeSearcher) Search(_ context.Context, _ string, page int) (*flibusta.SearchResults, error) {
	f.calls = append(f.calls, page)
	start := int64(page*50 + 1)
	end := min(start+50, 563)
	books := make([]flibusta.SearchResultBook, 0, 50)
	for id := start; id < end; id++ {
		books = append(books, flibusta.SearchResultBook{
			ID:      id,
			Title:   "Книга",
			Authors: []string{"Автор"},
			URL:     "/b/" + itoa(id),
		})
	}
	return &flibusta.SearchResults{
		Books:   books,
		Total:   562,
		Page:    page,
		HasNext: end < 562,
		HasPrev: page > 0,
	}, nil
}

func TestUIPageIsSlicedFromServerPage(t *testing.T) {
	client := &fakeSearcher{}
	page, err := LoadSearchPage(context.Background(), client, "query", 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 562 || page.TotalPages != 113 || page.StartIndex() != 1 {
		t.Fatalf("unexpected page: %#v", page)
	}
	if len(page.Books) != 5 || page.Books[0].ID != 1 || page.Books[4].ID != 5 {
		t.Fatalf("unexpected books: %#v", page.Books)
	}
	if len(client.calls) != 1 || client.calls[0] != 0 {
		t.Fatalf("unexpected calls: %#v", client.calls)
	}
}

func TestPageCrossesServerBoundary(t *testing.T) {
	client := &fakeSearcher{}
	page, err := LoadSearchPage(context.Background(), client, "query", 1, 30)
	if err != nil {
		t.Fatal(err)
	}
	if page.StartIndex() != 31 || len(page.Books) != 30 {
		t.Fatalf("unexpected page: %#v", page)
	}
	if page.Books[0].ID != 31 || page.Books[29].ID != 60 {
		t.Fatalf("unexpected books: %#v", page.Books)
	}
	if len(client.calls) != 2 || client.calls[0] != 0 || client.calls[1] != 1 {
		t.Fatalf("unexpected calls: %#v", client.calls)
	}
}

func TestLastPageIsShort(t *testing.T) {
	client := &fakeSearcher{}
	page, err := LoadSearchPage(context.Background(), client, "query", 112, 5)
	if err != nil {
		t.Fatal(err)
	}
	if page.UIPage != 112 || page.StartIndex() != 561 || len(page.Books) != 2 {
		t.Fatalf("unexpected page: %#v", page)
	}
	if page.Books[0].ID != 561 || page.Books[1].ID != 562 {
		t.Fatalf("unexpected books: %#v", page.Books)
	}
}

func TestRenderedMessageFitsTelegramLimit(t *testing.T) {
	client := &fakeSearcher{}
	page, err := LoadSearchPage(context.Background(), client, "query", 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	text := RenderSearchResults(page, "query")
	if len([]rune(text)) >= 4096 {
		t.Fatalf("text is too long: %d", len([]rune(text)))
	}
	if !strings.Contains(text, "Страница 1 из 113") || !strings.Contains(text, "Книга") {
		t.Fatalf("unexpected text: %s", text)
	}
}

func TestRenderSearchUsesLocalNumbering(t *testing.T) {
	page := &SearchPage{
		Books: []flibusta.SearchResultBook{
			{ID: 6, Title: "Book 6"},
			{ID: 7, Title: "Book 7"},
		},
		Total:      562,
		UIPage:     1,
		TotalPages: 113,
		PageSize:   5,
	}
	text := RenderSearchResults(page, "query")
	if !strings.Contains(text, "<b>1. Book 6</b>") ||
		!strings.Contains(text, "<b>2. Book 7</b>") {
		t.Fatalf("unexpected local numbering: %s", text)
	}
	if strings.Contains(text, "<b>6. ") {
		t.Fatalf("global numbering leaked into the page: %s", text)
	}
	if !strings.Contains(text, "книги 6–7 из 562") {
		t.Fatalf("missing range: %s", text)
	}
}

func TestRenderSearchShowsRatings(t *testing.T) {
	average := 4.2
	file := 5
	page := &SearchPage{
		Books: []flibusta.SearchResultBook{
			{ID: 1, Title: "Rated", BookRatingAvg: &average, FileRating: &file},
			{ID: 2, Title: "Unrated"},
		},
		Total:      2,
		TotalPages: 1,
		PageSize:   5,
	}
	text := RenderSearchResults(page, "query")
	if !strings.Contains(text, "Оценка: ★ 4.2 · файл 5") {
		t.Fatalf("ratings are missing: %s", text)
	}
	if strings.Count(text, "Оценка:") != 1 {
		t.Fatalf("unexpected ratings count: %s", text)
	}
}

func TestRenderAuthorsPage(t *testing.T) {
	authors := []flibusta.AuthorResult{
		{ID: 1, Name: "Author One", Details: "Author One (10 книг)"},
		{ID: 2, Name: "Author Two", Details: "Author Two (2 книги)"},
	}
	text, visible, page, totalPages := RenderAuthorsPage("Author", authors, 12, 0)
	if len(visible) != 2 || page != 0 || totalPages != 1 {
		t.Fatalf("unexpected author page: %#v %d %d", visible, page, totalPages)
	}
	if !strings.Contains(text, "Найдено авторов: 2") ||
		!strings.Contains(text, "Author One (10 книг)") ||
		!strings.Contains(text, "Книг по названию: 12") {
		t.Fatalf("unexpected author page: %s", text)
	}
}

func TestRenderAuthorsPagePagination(t *testing.T) {
	authors := make([]flibusta.AuthorResult, 0, 12)
	for id := int64(1); id <= 12; id++ {
		authors = append(authors, flibusta.AuthorResult{ID: id, Name: "Author"})
	}
	text, visible, page, totalPages := RenderAuthorsPage("Author", authors, 12, 1)
	if page != 1 || totalPages != 2 || len(visible) != 2 {
		t.Fatalf("unexpected page: %d/%d %#v", page, totalPages, visible)
	}
	if !strings.Contains(text, "<b>11. Author</b>") ||
		!strings.Contains(text, "<b>12. Author</b>") ||
		!strings.Contains(text, "Страница 2 из 2") {
		t.Fatalf("unexpected author page: %s", text)
	}
}

func TestAuthorPageSlicesBooks(t *testing.T) {
	books := make([]flibusta.SearchResultBook, 0, 12)
	for id := int64(1); id <= 12; id++ {
		books = append(books, flibusta.SearchResultBook{ID: id, Title: "Book"})
	}
	page := SliceAuthorBooks("Author", books, 1, 5)
	if page.AuthorName != "Author" || page.Total != 12 || page.TotalPages != 3 {
		t.Fatalf("unexpected page: %#v", page)
	}
	if len(page.Books) != 5 || page.Books[0].ID != 6 || page.Books[4].ID != 10 {
		t.Fatalf("unexpected books: %#v", page.Books)
	}
}

func TestCoverLinkPreview(t *testing.T) {
	preview := CoverLinkPreview("https://mirror.example/cover.jpg")
	if preview.URL == nil || *preview.URL != "https://mirror.example/cover.jpg" {
		t.Fatalf("unexpected preview URL: %#v", preview)
	}
	if preview.PreferSmallMedia == nil || !*preview.PreferSmallMedia {
		t.Fatal("small media preference is missing")
	}
	if preview.ShowAboveText == nil || !*preview.ShowAboveText {
		t.Fatal("show-above-text is missing")
	}

}

func TestRenderingDropsBooksInsteadOfExceedingLimit(t *testing.T) {
	books := make([]flibusta.SearchResultBook, 0, 30)
	for id := int64(1); id <= 30; id++ {
		books = append(books, flibusta.SearchResultBook{
			ID:      id,
			Title:   strings.Repeat("Очень длинное название ", 30),
			Authors: []string{strings.Repeat("Очень длинный автор ", 10)},
		})
	}
	page := &SearchPage{Books: books, Total: 30, UIPage: 0, TotalPages: 1, PageSize: 30}
	text, visible := RenderSearchPage(page, "query")
	if len([]rune(text)) > renderLimit {
		t.Fatalf("render limit exceeded: %d", len([]rune(text)))
	}
	if len(visible) == 0 || len(visible) >= 30 {
		t.Fatalf("unexpected visible count: %d", len(visible))
	}
}

func TestEscKeepsTelegramHTMLSafe(t *testing.T) {
	escaped := Esc(`<b>&"'`)
	if escaped != `&lt;b&gt;&amp;"'` {
		t.Fatalf("unexpected text escape: %s", escaped)
	}
	quoted := Esc(`https://example/?a=1&b="2"`, true)
	if quoted != `https://example/?a=1&amp;b=&quot;2&quot;` {
		t.Fatalf("unexpected attribute escape: %s", quoted)
	}
}

func TestFilenameFor(t *testing.T) {
	name := FilenameFor(`Война/мир: "том" 1`, 42)
	if strings.ContainsAny(name, `/:*?"<>|`) {
		t.Fatalf("unsafe filename: %s", name)
	}
	if !strings.HasSuffix(name, ".epub") {
		t.Fatalf("missing extension: %s", name)
	}
}

func TestTruncateMessageUsesRunes(t *testing.T) {
	text := strings.Repeat("я", 5000)
	truncated := TruncateMessage(text)
	if len([]rune(truncated)) > telegramSafeLimit {
		t.Fatalf("truncated message is too long: %d", len([]rune(truncated)))
	}
	if !strings.HasSuffix(truncated, "<i>…</i>") {
		t.Fatal("truncation marker is missing")
	}
}

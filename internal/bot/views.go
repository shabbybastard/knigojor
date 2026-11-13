package bot

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/go-telegram/bot/models"

	"github.com/shabbybastard/knigojor/internal/flibusta"
)

const maxServerPageSize = 50
const authorsPageSize = 10
const renderLimit = 3900
const telegramSafeLimit = 4096 - 96

// SearchPage is one UI page sliced out of a Flibusta server page.
type SearchPage struct {
	Books      []flibusta.SearchResultBook
	Authors    []flibusta.AuthorResult
	Total      int
	UIPage     int
	TotalPages int
	PageSize   int
	AuthorName string
}

// StartIndex is the 1-based number of the first visible book.
func (p SearchPage) StartIndex() int { return p.UIPage*p.PageSize + 1 }

// HasPrev reports whether a previous UI page exists.
func (p SearchPage) HasPrev() bool { return p.UIPage > 0 }

// HasNext reports whether a next UI page exists.
func (p SearchPage) HasNext() bool { return p.UIPage < p.TotalPages-1 }

// Searcher is the Flibusta search surface used by the UI.
type Searcher interface {
	Search(ctx context.Context, query string, page int) (*flibusta.SearchResults, error)
}

// LoadSearchPage loads one UI page and may cross server-page boundaries.
func LoadSearchPage(ctx context.Context, client Searcher, query string, uiPage, pageSize int) (*SearchPage, error) {
	pageSize = max(1, min(pageSize, maxServerPageSize))
	uiPage = max(0, uiPage)
	fetch := func(serverPage int) (*flibusta.SearchResults, error) {
		return client.Search(ctx, query, serverPage)
	}

	// The site has changed its server page size over time, so detect it from
	// the first page instead of assuming a fixed value.
	results, err := fetch(0)
	if err != nil {
		return nil, err
	}
	serverSize := len(results.Books)
	if serverSize == 0 {
		serverSize = max(1, pageSize)
	}
	total := results.Total
	if total == 0 {
		total = len(results.Books)
	}
	totalPages := max(1, (total+pageSize-1)/pageSize)

	if uiPage >= totalPages {
		uiPage = totalPages - 1
	}

	start := uiPage * pageSize
	firstServer := start / serverSize
	lastServer := (start + pageSize - 1) / serverSize
	offset := start % serverSize

	books := make([]flibusta.SearchResultBook, 0, pageSize)
	for serverPage := firstServer; serverPage <= lastServer; serverPage++ {
		current := results
		if serverPage != 0 {
			current, err = fetch(serverPage)
			if err != nil {
				return nil, err
			}
		}
		if serverPage == firstServer {
			if offset < len(current.Books) {
				books = append(books, current.Books[offset:]...)
			}
		} else {
			books = append(books, current.Books...)
		}
	}
	if len(books) > pageSize {
		books = books[:pageSize]
	}

	return &SearchPage{
		Books:      books,
		Authors:    results.Authors,
		Total:      total,
		UIPage:     uiPage,
		TotalPages: totalPages,
		PageSize:   pageSize,
	}, nil
}

// SliceAuthorBooks builds a UI page from a cached author book list.
func SliceAuthorBooks(name string, books []flibusta.SearchResultBook, uiPage, pageSize int) *SearchPage {
	pageSize = max(1, pageSize)
	uiPage = max(0, uiPage)
	total := len(books)
	totalPages := max(1, (total+pageSize-1)/pageSize)
	if uiPage >= totalPages {
		uiPage = totalPages - 1
	}
	start := uiPage * pageSize
	end := min(start+pageSize, total)
	visible := make([]flibusta.SearchResultBook, 0, max(0, end-start))
	if start < total {
		visible = append(visible, books[start:end]...)
	}
	return &SearchPage{
		Books:      visible,
		Total:      total,
		UIPage:     uiPage,
		TotalPages: totalPages,
		PageSize:   pageSize,
		AuthorName: name,
	}
}

// RenderSearchPage drops trailing books until the text fits Telegram.
func RenderSearchPage(page *SearchPage, query string) (string, []flibusta.SearchResultBook) {
	books := append([]flibusta.SearchResultBook(nil), page.Books...)
	for len(books) > 0 {
		candidate := &SearchPage{
			Books:      books,
			Total:      page.Total,
			UIPage:     page.UIPage,
			TotalPages: page.TotalPages,
			PageSize:   page.PageSize,
			AuthorName: page.AuthorName,
		}
		text := RenderSearchResults(candidate, query)
		if utf16Len(text) <= renderLimit {
			return text, books
		}
		books = books[:len(books)-1]
	}
	empty := &SearchPage{
		Total:      page.Total,
		UIPage:     page.UIPage,
		TotalPages: page.TotalPages,
		PageSize:   page.PageSize,
		AuthorName: page.AuthorName,
	}
	return RenderSearchResults(empty, query), nil
}

// RenderSearchResults formats a search or author page.
func RenderSearchResults(page *SearchPage, query string) string {
	header := "<b>FLIBUSTA / SEARCH</b>"
	if page.AuthorName != "" {
		header = "<b>FLIBUSTA / AUTHOR</b>"
	}
	lines := []string{
		header,
		"",
	}
	if page.AuthorName != "" {
		lines = append(lines, "Автор: "+Esc(Clip(page.AuthorName, 300)))
	} else {
		lines = append(lines, "Запрос: "+Esc(Clip(query, 200)))
	}
	lines = append(lines, fmt.Sprintf("Найдено: %d", page.Total))
	if len(page.Books) > 0 {
		lines = append(lines, fmt.Sprintf(
			"Страница %d из %d · книги %d–%d из %d",
			page.UIPage+1,
			page.TotalPages,
			page.StartIndex(),
			page.StartIndex()+len(page.Books)-1,
			page.Total,
		))
	} else {
		lines = append(lines, fmt.Sprintf("Страница %d из %d", page.UIPage+1, page.TotalPages))
	}
	lines = append(lines, "")
	if len(page.Books) == 0 {
		lines = append(lines, "Ничего не найдено.")
		return strings.Join(lines, "\n")
	}
	for index, book := range page.Books {
		number := index + 1
		lines = append(lines, fmt.Sprintf("<b>%d. %s</b>", number, Esc(Clip(book.Title, 180))))
		if len(book.Authors) > 0 {
			lines = append(lines, "Автор: "+Esc(Clip(strings.Join(book.Authors, ", "), 180)))
		}
		if ratings := bookRatingLine(book); ratings != "" {
			lines = append(lines, "Оценка: "+ratings)
		}
		lines = append(lines, "")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func bookRatingLine(book flibusta.SearchResultBook) string {
	parts := make([]string, 0, 2)
	if book.BookRatingAvg != nil {
		parts = append(parts, fmt.Sprintf("★ %.1f", *book.BookRatingAvg))
	}
	if book.FileRating != nil {
		parts = append(parts, fmt.Sprintf("файл %d", *book.FileRating))
	}
	return strings.Join(parts, " · ")
}

// SliceAuthors returns one page of author matches.
func SliceAuthors(authors []flibusta.AuthorResult, page int) (
	visible []flibusta.AuthorResult,
	clampedPage int,
	totalPages int,
) {
	totalPages = max(1, (len(authors)+authorsPageSize-1)/authorsPageSize)
	page = max(0, min(page, totalPages-1))
	start := page * authorsPageSize
	end := min(start+authorsPageSize, len(authors))
	if start < len(authors) {
		visible = append(visible, authors[start:end]...)
	}
	return visible, page, totalPages
}

// RenderAuthorsPage formats author matches and returns the visible authors and
// clamped page. The keyboard must be built from the returned list.
func RenderAuthorsPage(
	query string,
	authors []flibusta.AuthorResult,
	totalBooks int,
	page int,
) (string, []flibusta.AuthorResult, int, int) {
	visible, page, totalPages := SliceAuthors(authors, page)
	for len(visible) > 0 {
		text := renderAuthors(query, visible, len(authors), totalBooks, page, totalPages)
		if utf16Len(text) <= renderLimit {
			return text, visible, page, totalPages
		}
		visible = visible[:len(visible)-1]
	}
	return renderAuthors(query, nil, len(authors), totalBooks, page, totalPages), nil, page, totalPages
}

func renderAuthors(
	query string,
	visible []flibusta.AuthorResult,
	totalAuthors int,
	totalBooks int,
	page int,
	totalPages int,
) string {
	lines := []string{
		"<b>FLIBUSTA / SEARCH</b>",
		"",
		"Запрос: " + Esc(Clip(query, 200)),
		fmt.Sprintf("Найдено авторов: %d", totalAuthors),
		fmt.Sprintf("Страница %d из %d", page+1, totalPages),
		"",
	}
	for index, author := range visible {
		display := author.Details
		if display == "" {
			display = author.Name
		}
		lines = append(lines, fmt.Sprintf(
			"<b>%d. %s</b>",
			page*authorsPageSize+index+1,
			Esc(Clip(display, 200)),
		))
	}
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("Книг по названию: %d", totalBooks))
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// CoverLinkPreview shows the cover as a small preview above the card.
// The URL must stay on CONTENT_BASE_URL (or one of its subdomains) so a
// compromised catalog cannot point Telegram at an arbitrary host.
func CoverLinkPreview(coverURL, contentBase string) *models.LinkPreviewOptions {
	if coverURL == "" || !isHTTPURL(coverURL) || !sameHost(coverURL, contentBase) {
		return disabledLinkPreview()
	}
	return &models.LinkPreviewOptions{
		URL:              boolStringPtr(coverURL),
		PreferSmallMedia: boolPtr(true),
		ShowAboveText:    boolPtr(true),
	}
}

func sameHost(rawURL, baseURL string) bool {
	cover, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	coverHost := strings.ToLower(cover.Hostname())
	baseHost := strings.ToLower(base.Hostname())
	if coverHost == "" || baseHost == "" {
		return false
	}
	if portOrDefault(cover) != portOrDefault(base) {
		return false
	}
	return coverHost == baseHost || strings.HasSuffix(coverHost, "."+baseHost)
}

func portOrDefault(parsed *url.URL) string {
	if port := parsed.Port(); port != "" {
		return port
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func boolStringPtr(value string) *string {
	return &value
}

func isHTTPURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// RenderBook formats a book card.
func RenderBook(info *flibusta.BookInfo) string {
	lines := []string{
		"<b>FLIBUSTA / BOOK</b>",
		"",
		"<b>" + Esc(Clip(info.Title, 300)) + "</b>",
	}
	if len(info.Authors) > 0 {
		lines = append(lines, "Автор: "+Esc(Clip(strings.Join(info.Authors, ", "), 300)))
	}
	if len(info.Genres) > 0 {
		lines = append(lines, "Жанр: "+Esc(Clip(strings.Join(info.Genres, ", "), 300)))
	}
	if info.BookRatingAvg != nil {
		rating := fmt.Sprintf("★ %.2f", *info.BookRatingAvg)
		if info.BookRatingCount != nil && *info.BookRatingCount > 0 {
			rating += fmt.Sprintf(" (%d)", *info.BookRatingCount)
		}
		lines = append(lines, "Рейтинг книги: "+rating)
	}
	if info.FileRating != nil {
		lines = append(lines, fmt.Sprintf("Рейтинг файла: %d", *info.FileRating))
	} else if info.FileRatingRaw != "" {
		lines = append(lines, "Рейтинг файла: "+Esc(Clip(info.FileRatingRaw, 100)))
	}
	if info.Size != "" {
		lines = append(lines, "Размер: "+Esc(Clip(info.Size, 40)))
	}
	if info.Pages != nil {
		lines = append(lines, fmt.Sprintf("Страниц: %d", *info.Pages))
	}
	if len(info.Formats) > 0 {
		lines = append(lines, "Форматы: "+Esc(strings.Join(info.Formats, ", ")))
	}
	if info.Annotation != "" {
		lines = append(lines, "")
		lines = append(lines, "<blockquote>"+Esc(Clip(info.Annotation, 1000))+"</blockquote>")
	}
	sourceURL := info.SourceURL
	if sourceURL == "" {
		sourceURL = fmt.Sprintf("https://flibusta.is/b/%d", info.ID)
	}
	lines = append(lines, "")
	lines = append(lines, `<a href="`+Esc(sourceURL, true)+`">Оригинальная страница</a>`)
	return strings.Join(lines, "\n")
}

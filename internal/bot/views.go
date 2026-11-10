package bot

import (
	"context"
	"fmt"
	"strings"

	"github.com/shabbybastard/knigojor/internal/flibusta"
)

const serverPageSize = 50
const renderLimit = 3900
const telegramSafeLimit = 4096 - 96

// SearchPage is one UI page sliced out of Flibusta's 50-book server pages.
type SearchPage struct {
	Books      []flibusta.SearchResultBook
	Total      int
	UIPage     int
	TotalPages int
	PageSize   int
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
	pageSize = max(1, min(pageSize, serverPageSize))
	uiPage = max(0, uiPage)

	serverPageFor := func(page int) int {
		return (page * pageSize) / serverPageSize
	}
	fetch := func(serverPage int) (*flibusta.SearchResults, error) {
		return client.Search(ctx, query, serverPage)
	}

	results, err := fetch(serverPageFor(uiPage))
	if err != nil {
		return nil, err
	}
	total := results.Total
	if total == 0 {
		total = len(results.Books)
	}
	totalPages := max(1, (total+pageSize-1)/pageSize)

	if uiPage >= totalPages {
		uiPage = totalPages - 1
		results, err = fetch(serverPageFor(uiPage))
		if err != nil {
			return nil, err
		}
		total = results.Total
		if total == 0 {
			total = len(results.Books)
		}
		totalPages = max(1, (total+pageSize-1)/pageSize)
	}

	start := uiPage * pageSize
	firstServer := start / serverPageSize
	lastServer := (start + pageSize - 1) / serverPageSize
	offset := start % serverPageSize

	books := make([]flibusta.SearchResultBook, 0, pageSize)
	for serverPage := firstServer; serverPage <= lastServer; serverPage++ {
		current := results
		if serverPage != firstServer {
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
		Total:      total,
		UIPage:     uiPage,
		TotalPages: totalPages,
		PageSize:   pageSize,
	}, nil
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
	}
	return RenderSearchResults(empty, query), nil
}

// RenderSearchResults formats a search page.
func RenderSearchResults(page *SearchPage, query string) string {
	lines := []string{
		"<b>FLIBUSTA / SEARCH</b>",
		"",
		"Запрос: " + Esc(Clip(query, 200)),
		fmt.Sprintf("Найдено: %d", page.Total),
		fmt.Sprintf("Страница %d из %d", page.UIPage+1, page.TotalPages),
		"",
	}
	if len(page.Books) == 0 {
		lines = append(lines, "Ничего не найдено.")
		return strings.Join(lines, "\n")
	}
	for index, book := range page.Books {
		number := page.StartIndex() + index
		lines = append(lines, fmt.Sprintf("<b>%d. %s</b>", number, Esc(Clip(book.Title, 180))))
		if len(book.Authors) > 0 {
			lines = append(lines, "Автор: "+Esc(Clip(strings.Join(book.Authors, ", "), 180)))
		}
		lines = append(lines, "")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
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
		lines = append(lines, "Форматы: "+strings.Join(info.Formats, ", "))
	}
	if info.Annotation != "" {
		lines = append(lines, "")
		lines = append(lines, "<blockquote>"+Esc(Clip(info.Annotation, 1000))+"</blockquote>")
	}
	if info.CoverURL != "" {
		lines = append(lines, "")
		lines = append(lines, `<a href="`+Esc(info.CoverURL, true)+`">Обложка</a>`)
	}
	sourceURL := info.SourceURL
	if sourceURL == "" {
		sourceURL = fmt.Sprintf("https://flibusta.is/b/%d", info.ID)
	}
	lines = append(lines, "")
	lines = append(lines, `<a href="`+Esc(sourceURL, true)+`">Оригинальная страница</a>`)
	return strings.Join(lines, "\n")
}

// Package flibusta implements the Flibusta HTTP client and HTML parser.
package flibusta

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

const defaultBase = "https://flisland.net"

// SearchResultBook is one book row in search results.
type SearchResultBook struct {
	ID            int64
	Title         string
	Authors       []string
	URL           string
	FileRating    *int
	BookRatingAvg *float64
}

// AuthorResult is one author row in search results.
type AuthorResult struct {
	ID      int64
	Name    string
	Details string
}

// AuthorBooks is the full book list of an author page.
type AuthorBooks struct {
	ID    int64
	Name  string
	Books []SearchResultBook
}

// SearchResults is one parsed server search page.
type SearchResults struct {
	Books   []SearchResultBook
	Authors []AuthorResult
	Total   int
	Page    int
	HasNext bool
	HasPrev bool
}

// BookInfo is the parsed book card.
type BookInfo struct {
	ID              int64
	Title           string
	Authors         []string
	Genres          []string
	Annotation      string
	CoverURL        string
	FileRating      *int
	FileRatingRaw   string
	Size            string
	Pages           *int
	BookRatingAvg   *float64
	BookRatingCount *int
	Formats         []string
	Added           string
	HasFB2          bool
	HasEPUB         bool
	HasMOBI         bool
	SourceURL       string
}

var (
	totalWithRange = regexp.MustCompile(`Найденные книги\s*\([^)]*из\s*(\d+)\s*\)`)
	totalFallback  = regexp.MustCompile(`Найденные книги[^)]*из\s*(\d+)`)
	bookHref       = regexp.MustCompile(`^/b/(\d+)$`)
	authorHref     = regexp.MustCompile(`^/a/(\d+)$`)
	titleSuffix    = regexp.MustCompile(`\s*\((fb2|epub|mobi)\)\s*$`)
	sizePages      = regexp.MustCompile(`(\d+(?:\.\d+)?[KMkm])\s*,\s*(\d+)\s*с\.`)
	ratingLine     = regexp.MustCompile(`Оценки:\s*(\d+)\s*,\s*от\s*\d+\s*до\s*\d+\s*,\s*среднее\s*([\d.]+)`)
	addedLine      = regexp.MustCompile(`Добавлена:\s*([0-9.]+)`)
	fileRatingAlt  = regexp.MustCompile(`на\s*(\d)`)
	firstDigit     = regexp.MustCompile(`(\d)`)
	fileRatingIcon = regexp.MustCompile(`/img/znak(\d+)\.gif`)
	averageRating  = regexp.MustCompile(`Средняя оценка:\s*([\d.]+)`)
)

var blockTags = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"div": true, "dl": true, "fieldset": true, "figcaption": true,
	"figure": true, "footer": true, "form": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "header": true,
	"hr": true, "li": true, "main": true, "nav": true, "ol": true,
	"p": true, "pre": true, "section": true, "table": true, "tr": true,
	"ul": true,
}

// ParseSearch parses a Flibusta search results page.
func ParseSearch(pageHTML string, page int) (*SearchResults, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(pageHTML))
	if err != nil {
		return nil, fmt.Errorf("parse search HTML: %w", err)
	}

	result := &SearchResults{Page: page}

	doc.Find("h3").Each(func(_ int, h3 *goquery.Selection) {
		text := nodeText(h3)
		if strings.Contains(text, "Найденные книги") {
			if match := totalWithRange.FindStringSubmatch(text); match != nil {
				result.Total, _ = strconv.Atoi(match[1])
			}
		}
	})

	if result.Total == 0 {
		if match := totalFallback.FindStringSubmatch(pageHTML); match != nil {
			result.Total, _ = strconv.Atoi(match[1])
		}
	}

	items := doc.Find("li")
	if resultsList := findSectionList(doc, "Найденные книги"); resultsList != nil {
		items = resultsList.Find("li")
	}

	seen := map[int64]bool{}
	items.Each(func(_ int, li *goquery.Selection) {
		var (
			bookID int64
			title  string
		)
		li.Find("a[href^='/b/']").EachWithBreak(func(_ int, a *goquery.Selection) bool {
			href, _ := a.Attr("href")
			match := bookHref.FindStringSubmatch(href)
			if match == nil {
				return true
			}
			bookID, _ = strconv.ParseInt(match[1], 10, 64)
			title = nodeText(a)
			return false
		})
		if bookID == 0 || seen[bookID] {
			return
		}
		seen[bookID] = true

		authors := make([]string, 0, 2)
		li.Find("a[href^='/a/']").Each(func(_ int, a *goquery.Selection) {
			href, _ := a.Attr("href")
			if href == "/a/all" {
				return
			}
			name := nodeText(a)
			if name != "" && !contains(authors, name) {
				authors = append(authors, name)
			}
		})

		result.Books = append(result.Books, SearchResultBook{
			ID:      bookID,
			Title:   title,
			Authors: authors,
			URL:     fmt.Sprintf("/b/%d", bookID),
		})
	})

	seenAuthors := map[int64]bool{}
	if authorsList := findSectionList(doc, "Найденные писатели"); authorsList != nil {
		authorsList.Find("li").Each(func(_ int, li *goquery.Selection) {
			a := li.Find("a[href^='/a/']").First()
			if a.Length() == 0 {
				return
			}
			href, _ := a.Attr("href")
			match := authorHref.FindStringSubmatch(href)
			if match == nil {
				return
			}
			authorID, _ := strconv.ParseInt(match[1], 10, 64)
			if authorID == 0 || seenAuthors[authorID] {
				return
			}
			name := nodeText(a)
			if name == "" {
				return
			}
			seenAuthors[authorID] = true
			result.Authors = append(result.Authors, AuthorResult{
				ID:      authorID,
				Name:    name,
				Details: nodeText(li),
			})
		})
	}

	doc.Find("a").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		if !strings.Contains(href, "page=") {
			return
		}
		text := strings.ToLower(nodeText(a))
		switch {
		case strings.Contains(text, "следующ"), text == "next", text == ">", text == "›":
			result.HasNext = true
		case strings.Contains(text, "предыдущ"), text == "prev", text == "<", text == "‹":
			result.HasPrev = true
		}
	})

	if result.Total == 0 {
		result.Total = len(result.Books)
	}
	return result, nil
}

func findSectionList(doc *goquery.Document, heading string) *goquery.Selection {
	var list *goquery.Selection
	doc.Find("h3").EachWithBreak(func(_ int, h3 *goquery.Selection) bool {
		if !strings.Contains(nodeText(h3), heading) {
			return true
		}
		for node := h3.Next(); node.Length() > 0; node = node.Next() {
			tag := goquery.NodeName(node)
			if tag == "ul" {
				list = node
				break
			}
			if tag == "h2" || tag == "h3" {
				break
			}
		}
		return false
	})
	return list
}

// ParseAuthorBooks parses the book list from an author page.
func ParseAuthorBooks(pageHTML string, authorID int64) (*AuthorBooks, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(pageHTML))
	if err != nil {
		return nil, fmt.Errorf("parse author HTML: %w", err)
	}
	result := &AuthorBooks{ID: authorID}
	result.Name = nodeText(doc.Find("h1.title").First())

	content := doc.Find("#main").First()
	if content.Length() == 0 {
		content = doc.Find("body").First()
	}

	seen := map[int64]bool{}
	content.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		match := bookHref.FindStringSubmatch(href)
		if match == nil {
			return
		}
		bookID, _ := strconv.ParseInt(match[1], 10, 64)
		title := nodeText(a)
		if bookID == 0 || title == "" || seen[bookID] {
			return
		}
		seen[bookID] = true
		fileRating, bookRating := parseAuthorBookRatings(a)
		result.Books = append(result.Books, SearchResultBook{
			ID:            bookID,
			Title:         title,
			URL:           fmt.Sprintf("/b/%d", bookID),
			FileRating:    fileRating,
			BookRatingAvg: bookRating,
		})
	})
	if result.Name == "" || len(result.Books) == 0 {
		return nil, fmt.Errorf("author page not found or empty")
	}
	return result, nil
}

// parseAuthorBookRatings reads the rating icons that immediately precede a
// book link on an author page.
func parseAuthorBookRatings(a *goquery.Selection) (*int, *float64) {
	if a == nil || len(a.Nodes) == 0 {
		return nil, nil
	}
	var fileRating *int
	var bookRating *float64
	for node := a.Nodes[0].PrevSibling; node != nil; node = node.PrevSibling {
		if node.Type == html.TextNode {
			text := strings.TrimSpace(node.Data)
			// A book row contains a separator like " - 1. " between the
			// rating icons and the title link.
			if strings.Trim(text, "-—.0123456789 \t") == "" {
				continue
			}
			break
		}
		if node.Type != html.ElementNode {
			continue
		}
		switch node.Data {
		case "br":
			return fileRating, bookRating
		case "input":
			continue
		case "img":
			src, _ := nodeAttr(node, "src")
			if match := fileRatingIcon.FindStringSubmatch(src); match != nil {
				value, err := strconv.Atoi(match[1])
				if err == nil {
					fileRating = &value
				}
				continue
			}
			return fileRating, bookRating
		case "svg":
			if match := averageRating.FindStringSubmatch(nodeTitleText(node)); match != nil {
				value, err := strconv.ParseFloat(match[1], 64)
				if err == nil {
					bookRating = &value
				}
			}
		default:
			return fileRating, bookRating
		}
	}
	return fileRating, bookRating
}

func nodeAttr(node *html.Node, name string) (string, bool) {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val, true
		}
	}
	return "", false
}

func nodeTitleText(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && current.Data == "title" {
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == html.TextNode {
					builder.WriteString(child.Data)
				}
			}
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(builder.String()), " ")
}

// ParseBook parses a Flibusta book page.
func ParseBook(pageHTML string, bookID int64, baseURL string) (*BookInfo, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(pageHTML))
	if err != nil {
		return nil, fmt.Errorf("parse book HTML: %w", err)
	}
	if baseURL == "" {
		baseURL = defaultBase
	}
	info := &BookInfo{
		ID:        bookID,
		SourceURL: strings.TrimRight(baseURL, "/") + fmt.Sprintf("/b/%d", bookID),
	}

	h1 := doc.Find("h1.title").First()
	if h1.Length() > 0 {
		title := nodeText(h1)
		info.Title = strings.TrimSpace(titleSuffix.ReplaceAllString(title, ""))
	}

	info.Authors, info.Genres = parseAuthorsAndGenres(h1)

	doc.Find("img").EachWithBreak(func(_ int, img *goquery.Selection) bool {
		src, _ := img.Attr("src")
		if !strings.Contains(src, "/img/znak") {
			return true
		}
		alt, _ := img.Attr("alt")
		if alt == "" {
			alt, _ = img.Attr("title")
		}
		info.FileRatingRaw = alt
		info.FileRating = parseFileRatingAlt(alt)
		return false
	})

	content := doc.Find("#main").First()
	if content.Length() == 0 {
		content = doc.Find("body").First()
	}
	bodyText := normalizeText(content)

	if match := sizePages.FindStringSubmatch(bodyText); match != nil {
		info.Size = match[1]
		if pages, err := strconv.Atoi(match[2]); err == nil {
			info.Pages = &pages
		}
	}

	idText := strconv.FormatInt(bookID, 10)
	doc.Find("a").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		if strings.Contains(href, "/b/"+idText+"/fb2") {
			info.HasFB2 = true
			if !contains(info.Formats, "FB2") {
				info.Formats = append(info.Formats, "FB2")
			}
		}
		if strings.Contains(href, "/b/"+idText+"/epub") {
			info.HasEPUB = true
			if !contains(info.Formats, "EPUB") {
				info.Formats = append(info.Formats, "EPUB")
			}
		}
		if strings.Contains(href, "/b/"+idText+"/mobi") {
			info.HasMOBI = true
			if !contains(info.Formats, "MOBI") {
				info.Formats = append(info.Formats, "MOBI")
			}
		}
	})

	info.Annotation = parseAnnotation(doc)
	info.CoverURL = parseCover(content, bookID, baseURL)

	if match := ratingLine.FindStringSubmatch(bodyText); match != nil {
		count, _ := strconv.Atoi(match[1])
		avg, _ := strconv.ParseFloat(match[2], 64)
		info.BookRatingCount = &count
		info.BookRatingAvg = &avg
	}
	if match := addedLine.FindStringSubmatch(bodyText); match != nil {
		info.Added = match[1]
	}
	return info, nil
}

func parseAuthorsAndGenres(h1 *goquery.Selection) ([]string, []string) {
	authors := make([]string, 0, 2)
	genres := make([]string, 0, 2)
	if h1 == nil || h1.Length() == 0 {
		return authors, genres
	}

	for node := h1.Next(); node.Length() > 0; node = node.Next() {
		tag := goquery.NodeName(node)
		if tag == "h2" && strings.Contains(nodeText(node), "Аннотация") {
			break
		}
		if tag == "h2" || tag == "hr" {
			break
		}

		var links *goquery.Selection
		switch tag {
		case "a":
			links = node
		case "div", "p", "span", "li":
			links = node.Find("a")
		}
		if links == nil {
			continue
		}
		links.Each(func(_ int, a *goquery.Selection) {
			href, _ := a.Attr("href")
			text := nodeText(a)
			if text == "" {
				return
			}
			switch {
			case strings.HasPrefix(href, "/a/") && href != "/a/all":
				if !contains(authors, text) {
					authors = append(authors, text)
				}
			case strings.HasPrefix(href, "/g/"):
				if !contains(genres, text) {
					genres = append(genres, text)
				}
			}
		})
	}
	return authors, genres
}

func parseAnnotation(doc *goquery.Document) string {
	var annotation string
	doc.Find("h2").EachWithBreak(func(_ int, h2 *goquery.Selection) bool {
		if !strings.HasPrefix(nodeText(h2), "Аннотация") {
			return true
		}
		parts := make([]string, 0, 4)
		for node := h2.Next(); node.Length() > 0; node = node.Next() {
			tag := goquery.NodeName(node)
			if tag == "h2" || tag == "hr" {
				break
			}
			if tag == "p" || tag == "div" {
				if text := cleanLines(rawText(node)); text != "" {
					parts = append(parts, text)
				}
			}
		}
		annotation = clipRunes(strings.Join(parts, "\n"), 2000)
		return false
	})
	return annotation
}

func parseCover(content *goquery.Selection, bookID int64, baseURL string) string {
	type image struct {
		src string
		alt string
	}
	images := make([]image, 0)
	content.Find("img").Each(func(_ int, img *goquery.Selection) {
		src, _ := img.Attr("src")
		if !strings.Contains(src, "/i/") {
			return
		}
		alt, _ := img.Attr("alt")
		images = append(images, image{src: src, alt: alt})
	})
	for _, item := range images {
		if strings.Contains(strings.ToLower(item.alt), "cover") {
			return resolveURL(item.src, baseURL)
		}
	}
	idText := strconv.FormatInt(bookID, 10)
	for _, item := range images {
		if strings.Contains(item.src, idText) {
			return resolveURL(item.src, baseURL)
		}
	}
	if len(images) > 0 {
		return resolveURL(images[0].src, baseURL)
	}
	return ""
}

func parseFileRatingAlt(alt string) *int {
	alt = strings.ToLower(strings.TrimSpace(alt))
	if strings.Contains(alt, "не оценен") {
		return nil
	}
	if match := fileRatingAlt.FindStringSubmatch(alt); match != nil {
		value, _ := strconv.Atoi(match[1])
		return &value
	}
	if match := firstDigit.FindStringSubmatch(alt); match != nil {
		value, _ := strconv.Atoi(match[1])
		return &value
	}
	return nil
}

func resolveURL(raw, base string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	if parsed.IsAbs() {
		return parsed.String()
	}
	baseURL, err := url.Parse(strings.TrimRight(base, "/") + "/")
	if err != nil {
		return raw
	}
	return baseURL.ResolveReference(parsed).String()
}

func nodeText(selection *goquery.Selection) string {
	if selection == nil || selection.Length() == 0 {
		return ""
	}
	return strings.Join(strings.Fields(rawText(selection)), " ")
}

func normalizeText(selection *goquery.Selection) string {
	return strings.Join(strings.Fields(rawText(selection)), " ")
}

func cleanLines(text string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func rawText(selection *goquery.Selection) string {
	if selection == nil {
		return ""
	}
	var builder strings.Builder
	for _, node := range selection.Nodes {
		writeNodeText(&builder, node)
	}
	return builder.String()
}

func writeNodeText(builder *strings.Builder, node *html.Node) {
	switch node.Type {
	case html.TextNode:
		builder.WriteString(node.Data)
		return
	case html.ElementNode:
		if node.Data == "br" {
			builder.WriteByte('\n')
			return
		}
		block := blockTags[node.Data]
		if block {
			builder.WriteByte('\n')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			writeNodeText(builder, child)
		}
		if block {
			builder.WriteByte('\n')
		}
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func clipRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimRight(string(runes[:limit]), " ")
}

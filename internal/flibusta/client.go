package flibusta

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/shabbybastard/knigojor/internal/netx"
)

// Client fetches and parses Flibusta pages from the content mirror.
type Client struct {
	contentBase string
	http        *netx.Client
}

// NewClient creates a Flibusta client for the given content base URL.
func NewClient(contentBase string, http *netx.Client) *Client {
	return &Client{
		contentBase: strings.TrimRight(contentBase, "/"),
		http:        http,
	}
}

// ContentBase returns the base URL used for HTML pages.
func (c *Client) ContentBase() string {
	return c.contentBase
}

// Search runs a Flibusta book search, including author and series sections.
func (c *Client) Search(ctx context.Context, query string, page int) (*SearchResults, error) {
	escapedQuery := strings.ReplaceAll(url.QueryEscape(query), "+", "%20")
	path := fmt.Sprintf(
		"/booksearch?ask=%s&cha=1&chb=1&chs=1&page=%d",
		escapedQuery,
		page,
	)
	body, err := c.http.GetText(ctx, c.contentBase+path)
	if err != nil {
		return nil, err
	}
	return ParseSearch(body, page)
}

// GetBook fetches and parses a book card.
func (c *Client) GetBook(ctx context.Context, bookID int64) (*BookInfo, error) {
	body, err := c.http.GetText(ctx, c.contentBase+fmt.Sprintf("/b/%d", bookID))
	if err != nil {
		return nil, err
	}
	return ParseBook(body, bookID, c.contentBase)
}

// GetAuthorBooks fetches all books listed on an author page.
func (c *Client) GetAuthorBooks(ctx context.Context, authorID int64) (*AuthorBooks, error) {
	body, err := c.http.GetText(ctx, c.contentBase+fmt.Sprintf("/a/%d?order=a", authorID))
	if err != nil {
		return nil, err
	}
	return ParseAuthorBooks(body, authorID)
}

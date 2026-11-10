package flibusta

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/shabbybastard/knigojor/internal/netx"
)

// Client fetches and parses Flibusta pages through the mirror manager.
type Client struct {
	mirrors    *netx.MirrorManager
	httpDirect *netx.Client
	httpTor    *netx.Client
}

// NewClient creates a Flibusta client.
func NewClient(mirrors *netx.MirrorManager, httpDirect, httpTor *netx.Client) *Client {
	return &Client{mirrors: mirrors, httpDirect: httpDirect, httpTor: httpTor}
}

// HTTPFor returns the HTTP client suitable for a mirror.
func (c *Client) HTTPFor(mirror *netx.Mirror) (*netx.Client, error) {
	if mirror.IsOnion() {
		if c.httpTor == nil {
			return nil, fmt.Errorf("Tor is not available for onion mirror")
		}
		return c.httpTor, nil
	}
	return c.httpDirect, nil
}

// Search runs a Flibusta book search.
func (c *Client) Search(ctx context.Context, query string, page int) (*SearchResults, error) {
	escapedQuery := strings.ReplaceAll(url.QueryEscape(query), "+", "%20")
	path := fmt.Sprintf("/booksearch?ask=%s&page=%d", escapedQuery, page)
	result, _, err := netx.RunWithFailover(ctx, c.mirrors, func(ctx context.Context, mirror *netx.Mirror) (*SearchResults, error) {
		http, err := c.HTTPFor(mirror)
		if err != nil {
			return nil, err
		}
		body, err := http.GetText(ctx, mirror.URL()+path)
		if err != nil {
			return nil, err
		}
		return ParseSearch(body, page)
	})
	return result, err
}

// GetBook fetches and parses a book card.
func (c *Client) GetBook(ctx context.Context, bookID int64) (*BookInfo, error) {
	path := fmt.Sprintf("/b/%d", bookID)
	result, _, err := netx.RunWithFailover(ctx, c.mirrors, func(ctx context.Context, mirror *netx.Mirror) (*BookInfo, error) {
		http, err := c.HTTPFor(mirror)
		if err != nil {
			return nil, err
		}
		body, err := http.GetText(ctx, mirror.URL()+path)
		if err != nil {
			return nil, err
		}
		return ParseBook(body, bookID, mirror.URL())
	})
	return result, err
}

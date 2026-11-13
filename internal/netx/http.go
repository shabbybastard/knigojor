package netx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/net/html/charset"
)

var (
	ErrFileTooLarge = errors.New("file too large")
	ErrPageTooLarge = errors.New("page too large")
)

// maxPageBytes bounds HTML responses from the catalog. Author pages can be
// large, but 8 MiB is far above anything the site currently serves.
const maxPageBytes = 8 << 20

// ProgressFunc receives downloaded and total bytes. total is -1 when the
// server does not provide Content-Length.
type ProgressFunc func(written, total int64)

// flisland.net delays requests from obvious bot user agents, so identify as a
// regular browser-compatible client.
const defaultUserAgent = "Mozilla/5.0 (compatible; knigojor/0.2)"

// StatusError is a non-2xx HTTP response.
type StatusError struct {
	Code   int
	Status string
	URL    string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected status %s for %s", e.Status, e.URL)
}

// Client is a small retrying HTTP client.
type Client struct {
	client     *http.Client
	transport  *http.Transport
	timeout    time.Duration
	maxRetries int
	userAgent  string
}

// NewClient creates a direct HTTP client.
func NewClient(timeout time.Duration, maxConns, maxRetries int) (*Client, error) {
	if maxConns < 1 {
		maxConns = 1
	}
	transport := newTransport(maxConns, timeout)
	return &Client{
		client: &http.Client{
			Transport: transport,
			// Per-request contexts control timeouts. A client-wide timeout
			// would incorrectly cap long downloads at HTTP_TIMEOUT.
		},
		transport:  transport,
		timeout:    timeout,
		maxRetries: max(0, maxRetries),
		userAgent:  defaultUserAgent,
	}, nil
}

func newTransport(maxConns int, timeout time.Duration) *http.Transport {
	return &http.Transport{
		MaxIdleConns:          maxConns * 2,
		MaxIdleConnsPerHost:   maxConns,
		MaxConnsPerHost:       maxConns,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		ForceAttemptHTTP2:     true,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
}

// Close releases idle connections.
func (c *Client) Close() {
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

func (c *Client) request(ctx context.Context, method, rawURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	return req, nil
}

func (c *Client) withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = c.timeout
	}
	return context.WithTimeout(ctx, timeout)
}

// GetText fetches a URL and returns the body as text.
func (c *Client) GetText(ctx context.Context, rawURL string) (string, error) {
	var body string
	err := Retry(ctx, c.maxRetries+1, func(ctx context.Context) error {
		reqCtx, cancel := c.withTimeout(ctx, 0)
		defer cancel()
		req, err := c.request(reqCtx, http.MethodGet, rawURL)
		if err != nil {
			return err
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return &StatusError{Code: resp.StatusCode, Status: resp.Status, URL: rawURL}
		}
		reader, err := charset.NewReader(resp.Body, resp.Header.Get("Content-Type"))
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(reader, maxPageBytes+1))
		if err != nil {
			return err
		}
		if len(data) > maxPageBytes {
			return ErrPageTooLarge
		}
		body = string(data)
		return nil
	}, IsRetryable)
	return body, err
}

// Get performs a single GET request. The caller owns the response body.
func (c *Client) Get(ctx context.Context, rawURL string, timeout time.Duration) (*http.Response, error) {
	reqCtx, cancel := c.withTimeout(ctx, timeout)
	req, err := c.request(reqCtx, http.MethodGet, rawURL)
	if err != nil {
		cancel()
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	// Tie cancellation to body close so the timeout is released properly.
	resp.Body = &cancelReadCloser{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// Download streams a URL into dest using the client retry policy.
func (c *Client) Download(ctx context.Context, rawURL, dest string, maxBytes int64, timeout time.Duration) (int64, error) {
	return c.DownloadAttempts(ctx, rawURL, dest, maxBytes, timeout, c.maxRetries+1)
}

// DownloadOnce streams a URL into dest exactly once.
func (c *Client) DownloadOnce(ctx context.Context, rawURL, dest string, maxBytes int64, timeout time.Duration) (int64, error) {
	return c.DownloadAttempts(ctx, rawURL, dest, maxBytes, timeout, 1)
}

// DownloadOnceProgress streams a URL into dest exactly once and reports
// download progress.
func (c *Client) DownloadOnceProgress(
	ctx context.Context,
	rawURL, dest string,
	maxBytes int64,
	timeout time.Duration,
	progress ProgressFunc,
) (int64, error) {
	return c.downloadAttempts(ctx, rawURL, dest, maxBytes, timeout, 1, progress)
}

// DownloadAttempts streams a URL into dest with an explicit attempt budget.
func (c *Client) DownloadAttempts(
	ctx context.Context,
	rawURL, dest string,
	maxBytes int64,
	timeout time.Duration,
	attempts int,
) (int64, error) {
	return c.downloadAttempts(ctx, rawURL, dest, maxBytes, timeout, attempts, nil)
}

func (c *Client) downloadAttempts(
	ctx context.Context,
	rawURL, dest string,
	maxBytes int64,
	timeout time.Duration,
	attempts int,
	progress ProgressFunc,
) (int64, error) {
	var written int64
	err := Retry(ctx, attempts, func(ctx context.Context) error {
		written = 0
		reqCtx, cancel := c.withTimeout(ctx, timeout)
		defer cancel()
		req, err := c.request(reqCtx, http.MethodGet, rawURL)
		if err != nil {
			return err
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return &StatusError{Code: resp.StatusCode, Status: resp.Status, URL: rawURL}
		}
		total := resp.ContentLength
		if progress != nil {
			progress(0, total)
		}

		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		file, err := os.Create(dest)
		if err != nil {
			return err
		}
		ok := false
		defer func() {
			_ = file.Close()
			if !ok {
				_ = os.Remove(dest)
			}
		}()

		buf := make([]byte, 64*1024)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				written += int64(n)
				if maxBytes > 0 && written > maxBytes {
					return ErrFileTooLarge
				}
				if _, err := file.Write(buf[:n]); err != nil {
					return err
				}
				if progress != nil {
					progress(written, total)
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
		ok = true
		return nil
	}, IsRetryable)
	if err != nil {
		_ = os.Remove(dest)
		return 0, err
	}
	return written, nil
}

// IsRetryable reports whether an error is worth another attempt.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrFileTooLarge) || errors.Is(err, ErrPageTooLarge) || errors.Is(err, context.Canceled) {
		return false
	}
	var statusErr *StatusError
	if errors.As(err, &statusErr) {
		return statusErr.Code == http.StatusTooManyRequests || statusErr.Code >= 500
	}
	return errors.Is(err, context.DeadlineExceeded) || isNetworkError(err)
}

func isNetworkError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}

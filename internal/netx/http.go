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
	"strings"
	"time"

	"golang.org/x/net/html/charset"
	"golang.org/x/net/proxy"
)

var ErrFileTooLarge = errors.New("file too large")

// StatusError is a non-2xx HTTP response.
type StatusError struct {
	Code   int
	Status string
	URL    string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected status %s for %s", e.Status, e.URL)
}

// Client is a small retrying HTTP client with optional SOCKS5 support.
type Client struct {
	client     *http.Client
	transport  *http.Transport
	timeout    time.Duration
	maxRetries int
	userAgent  string
}

// NewClient creates an HTTP client. proxyURL may be empty, an http(s) proxy or
// a socks5:// URL.
func NewClient(timeout time.Duration, maxConns int, proxyURL string, maxRetries int) (*Client, error) {
	if maxConns < 1 {
		maxConns = 1
	}
	transport, err := newTransport(maxConns, timeout, proxyURL)
	if err != nil {
		return nil, err
	}
	return &Client{
		client: &http.Client{
			Transport: transport,
			// Per-request contexts control timeouts. A client-wide timeout
			// would incorrectly cap long downloads at HTTP_TIMEOUT.
		},
		transport:  transport,
		timeout:    timeout,
		maxRetries: max(0, maxRetries),
		userAgent:  "knigojor/0.1",
	}, nil
}

func newTransport(maxConns int, timeout time.Duration, proxyURL string) (*http.Transport, error) {
	transport := &http.Transport{
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
	if proxyURL == "" {
		return transport, nil
	}

	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse proxy URL: %w", err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		transport.Proxy = http.ProxyURL(parsed)
		return transport, nil
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if parsed.User != nil {
			password, _ := parsed.User.Password()
			auth = &proxy.Auth{User: parsed.User.Username(), Password: password}
		}
		dialer, err := proxy.SOCKS5("tcp", parsed.Host, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("create SOCKS5 dialer: %w", err)
		}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
				return contextDialer.DialContext(ctx, network, address)
			}
			type dialResult struct {
				conn net.Conn
				err  error
			}
			result := make(chan dialResult, 1)
			go func() {
				conn, err := dialer.Dial(network, address)
				result <- dialResult{conn: conn, err: err}
			}()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case value := <-result:
				return value.conn, value.err
			}
		}
		return transport, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", parsed.Scheme)
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
		data, err := io.ReadAll(reader)
		if err != nil {
			return err
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

// Download streams a URL into dest. It enforces maxBytes while reading and
// removes the partial file on failure.
func (c *Client) Download(ctx context.Context, rawURL, dest string, maxBytes int64, timeout time.Duration) (int64, error) {
	var written int64
	err := Retry(ctx, c.maxRetries+1, func(ctx context.Context) error {
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
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
		if err := file.Sync(); err != nil {
			return err
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
	if errors.Is(err, ErrFileTooLarge) || errors.Is(err, context.Canceled) {
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

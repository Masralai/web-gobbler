package scraper

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/chromedp"
)

// Fetcher retrieves raw HTML for a URL after SSRF checks are done by the caller.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string, opts *ScrapeOptions) (body []byte, status int, err error)
}

type httpMeta struct {
	finalURL    string
	contentType string
}

// HTTPFetcher is the default plain HTTP fetcher.
type HTTPFetcher struct{}

func (HTTPFetcher) Fetch(ctx context.Context, rawURL string, opts *ScrapeOptions) ([]byte, int, error) {
	body, status, _, err := fetchHTTP(ctx, rawURL, opts)
	return body, status, err
}

func fetchHTTP(ctx context.Context, rawURL string, opts *ScrapeOptions) ([]byte, int, httpMeta, error) {
	if opts == nil {
		opts = DefaultOptions()
	}
	current := rawURL
	redirects := 0
	for {
		parsed, pinned, err := resolvePublicTarget(ctx, current)
		if err != nil {
			return nil, 0, httpMeta{finalURL: current}, err
		}
		client := newPinnedHTTPClient(opts, pinned)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current, nil)
		if err != nil {
			return nil, 0, httpMeta{finalURL: current}, fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("User-Agent", opts.UserAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		for k, v := range opts.Headers {
			if k == "" {
				continue
			}
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, httpMeta{finalURL: current}, fmt.Errorf("%w: %w", ErrTimeout, err)
			}
			return nil, 0, httpMeta{finalURL: current}, fmt.Errorf("request failed: %w", err)
		}

		meta := httpMeta{finalURL: current, contentType: resp.Header.Get("Content-Type")}
		if resp.Request != nil && resp.Request.URL != nil {
			meta.finalURL = resp.Request.URL.String()
		}

		if isRedirectStatus(resp.StatusCode) {
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			if !opts.FollowRedirects {
				return nil, resp.StatusCode, meta, fmt.Errorf("%w: %d", ErrHTTPFailed, resp.StatusCode)
			}
			redirects++
			if redirects > 5 {
				return nil, resp.StatusCode, meta, fmt.Errorf("%w: %d redirects", ErrTooManyRedirects, redirects)
			}
			next, err := normalizeFinalURL(parsed, loc)
			if err != nil {
				return nil, resp.StatusCode, meta, fmt.Errorf("invalid redirect location: %w", err)
			}
			// Re-check and re-pin each redirect target (SEC-001).
			current = next
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, resp.StatusCode, meta, fmt.Errorf("%w: %d", ErrHTTPFailed, resp.StatusCode)
		}

		var bodyReader io.Reader = resp.Body
		if opts.MaxBodySize > 0 {
			bodyReader = io.LimitReader(resp.Body, opts.MaxBodySize)
		}
		body, err := io.ReadAll(bodyReader)
		resp.Body.Close()
		if err != nil {
			return nil, resp.StatusCode, meta, fmt.Errorf("%w: %w", ErrParseFailure, err)
		}
		return body, resp.StatusCode, meta, nil
	}
}

func isRedirectStatus(code int) bool {
	switch code {
	case http.StatusMultipleChoices, http.StatusMovedPermanently, http.StatusFound,
		http.StatusSeeOther, http.StatusNotModified, http.StatusUseProxy,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// ErrBrowserUnavailable is returned when render_js is requested but Chrome is not available.
var ErrBrowserUnavailable = fmt.Errorf("browser fetch unavailable: set CHROME_PATH or install Chromium; use compose profile browser")

var (
	browserOnce sync.Once
	browserOK   bool
)

// BrowserAvailable reports whether a Chrome/Chromium binary can be used.
func BrowserAvailable() bool {
	browserOnce.Do(func() {
		if os.Getenv("CHROME_PATH") != "" || os.Getenv("RENDER_JS_ENABLED") == "1" {
			browserOK = true
			return
		}
		// chromedp finds google-chrome / chromium on PATH in many images
		for _, c := range []string{"google-chrome", "chromium", "chromium-browser", "chrome"} {
			if _, err := execLookPath(c); err == nil {
				browserOK = true
				return
			}
		}
	})
	return browserOK
}

// execLookPath is a thin wrapper for testing.
var execLookPath = func(file string) (string, error) {
	return lookPath(file)
}

// BrowserFetcher loads pages with headless Chrome via chromedp.
type BrowserFetcher struct {
	sem chan struct{}
}

// NewBrowserFetcher limits concurrent browser sessions (default 2).
func NewBrowserFetcher(maxConcurrent int) *BrowserFetcher {
	if maxConcurrent < 1 {
		maxConcurrent = 2
	}
	return &BrowserFetcher{sem: make(chan struct{}, maxConcurrent)}
}

func (f *BrowserFetcher) Fetch(ctx context.Context, rawURL string, opts *ScrapeOptions) ([]byte, int, error) {
	if !BrowserAvailable() {
		return nil, 0, ErrBrowserUnavailable
	}
	select {
	case f.sem <- struct{}{}:
		defer func() { <-f.sem }()
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	if p := os.Getenv("CHROME_PATH"); p != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(p))
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()

	taskCtx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	taskCtx, cancelTimeout := context.WithTimeout(taskCtx, timeout)
	defer cancelTimeout()

	if err := chromedp.Do(taskCtx,
		chromedp.Navigate(rawURL),
		chromedp.WaitReady(chromedp.CSS("body")),
	); err != nil {
		if taskCtx.Err() != nil {
			return nil, 0, fmt.Errorf("%w: %w", ErrTimeout, err)
		}
		return nil, 0, fmt.Errorf("browser navigate: %w", err)
	}
	html, err := chromedp.Run(taskCtx, chromedp.OuterHTML(chromedp.CSS("html")))
	if err != nil {
		if taskCtx.Err() != nil {
			return nil, 0, fmt.Errorf("%w: %w", ErrTimeout, err)
		}
		return nil, 0, fmt.Errorf("browser navigate: %w", err)
	}
	body := []byte(html)
	if opts.MaxBodySize > 0 && int64(len(body)) > opts.MaxBodySize {
		body = body[:opts.MaxBodySize]
	}
	return body, http.StatusOK, nil
}

func parseHTML(body []byte) (*goquery.Document, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParseFailure, err)
	}
	return doc, nil
}

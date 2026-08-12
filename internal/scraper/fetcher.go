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

// HTTPFetcher is the default plain HTTP fetcher.
type HTTPFetcher struct{}

func (HTTPFetcher) Fetch(ctx context.Context, rawURL string, opts *ScrapeOptions) ([]byte, int, error) {
	client := newScrapeClient(opts)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", opts.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, fmt.Errorf("%w: %w", ErrTimeout, err)
		}
		return nil, 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("%w: %d", ErrHTTPFailed, resp.StatusCode)
	}

	var bodyReader io.Reader = resp.Body
	if opts.MaxBodySize > 0 {
		bodyReader = io.LimitReader(resp.Body, opts.MaxBodySize)
	}
	body, err := io.ReadAll(bodyReader)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("%w: %w", ErrParseFailure, err)
	}
	return body, resp.StatusCode, nil
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

	var html string
	err := chromedp.Run(taskCtx,
		chromedp.Navigate(rawURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	)
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

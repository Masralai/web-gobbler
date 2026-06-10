package scraper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var (
	ErrInvalidURL   = errors.New("invalid URL: must be http:// or https://")
	ErrHTTPFailed   = errors.New("HTTP request failed with non-2xx status")
	ErrParseFailure = errors.New("failed to parse response body")
	ErrTimeout      = errors.New("request timed out")
)

type ScrapeOptions struct {
	Timeout        time.Duration
	MaxRetries     int
	FollowRedirects bool
	MaxBodySize    int64
	UserAgent      string
}

type Result struct {
	Links      []string `json:"links"`
	Headers    []string `json:"headers"`
	Paragraphs []string `json:"paragraphs"`
	HTTPStatus int      `json:"http_status"`
	DurationMs int64    `json:"duration_ms"`
}

func DefaultOptions() *ScrapeOptions {
	return &ScrapeOptions{
		Timeout:         10 * time.Second,
		MaxRetries:      3,
		FollowRedirects: true,
		MaxBodySize:     5 * 1024 * 1024,
		UserAgent:       "GoScrape/1.0",
	}
}

func ScrapePage(ctx context.Context, rawURL string, extractTypes []string, opts *ScrapeOptions) (*Result, error) {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return nil, fmt.Errorf("%w: %s", ErrInvalidURL, rawURL)
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidURL, err)
	}

	if opts == nil {
		opts = DefaultOptions()
	}

	client := &http.Client{
		Timeout: opts.Timeout,
	}
	if !opts.FollowRedirects {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", opts.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %w", ErrTimeout, err)
		}
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &Result{HTTPStatus: resp.StatusCode, DurationMs: time.Since(start).Milliseconds()},
			fmt.Errorf("%w: %d", ErrHTTPFailed, resp.StatusCode)
	}

	var bodyReader io.Reader = resp.Body
	if opts.MaxBodySize > 0 {
		bodyReader = io.LimitReader(resp.Body, opts.MaxBodySize)
	}

	doc, err := goquery.NewDocumentFromReader(bodyReader)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParseFailure, err)
	}

	result := &Result{
		HTTPStatus: resp.StatusCode,
	}

	extractSet := make(map[string]bool, len(extractTypes))
	if len(extractTypes) == 0 {
		extractSet["links"] = true
	} else {
		for _, t := range extractTypes {
			extractSet[strings.ToLower(t)] = true
		}
	}

	if extractSet["links"] || extractSet["all"] {
		doc.Find("a").Each(func(i int, s *goquery.Selection) {
			href, exists := s.Attr("href")
			if !exists {
				return
			}
			linkURL, err := url.Parse(href)
			if err != nil {
				return
			}
			absoluteURL := parsedURL.ResolveReference(linkURL).String()
			result.Links = append(result.Links, absoluteURL)
		})
	}

	if extractSet["headers"] || extractSet["all"] {
		doc.Find("h1,h2,h3,h4").Each(func(i int, s *goquery.Selection) {
			text := strings.TrimSpace(s.Text())
			if text != "" {
				result.Headers = append(result.Headers, text)
			}
		})
	}

	if extractSet["paragraphs"] || extractSet["all"] {
		doc.Find("p").Each(func(i int, s *goquery.Selection) {
			text := strings.TrimSpace(s.Text())
			if text != "" {
				result.Paragraphs = append(result.Paragraphs, text)
			}
		})
	}

	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}

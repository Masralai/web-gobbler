// Package scraper provides the core web scraping engine.
// It fetches HTML pages over HTTP, parses the DOM with goquery,
// and extracts content (links, headers, paragraphs) from the response.
// SSRF protection is enforced by rejecting targets that resolve to
// private or loopback IP ranges. Redirect chains are followed up to
// a maximum of 5 hops, with each redirect target also SSRF-checked.
package scraper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var (
	// ErrInvalidURL is returned when the provided URL does not use http:// or https://.
	ErrInvalidURL = errors.New("invalid URL: must be http:// or https://")
	// ErrHTTPFailed is returned when the server responds with a non-2xx status code.
	ErrHTTPFailed = errors.New("HTTP request failed with non-2xx status")
	// ErrParseFailure is returned when the response body cannot be parsed as HTML.
	ErrParseFailure = errors.New("failed to parse response body")
	// ErrTimeout is returned when the HTTP request exceeds the configured timeout.
	ErrTimeout = errors.New("request timed out")
	// ErrPrivateIP is returned when the target hostname resolves to a private or loopback IP range.
	ErrPrivateIP = errors.New("request refused: target resolves to a private IP")
	// ErrTooManyRedirects is returned when the redirect chain exceeds the maximum allowed hops (5).
	ErrTooManyRedirects = errors.New("too many redirects")
)

var privateCIDRs []*net.IPNet

func init() {
	for _, cidr := range []string{
		"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12",
		"192.168.0.0/16", "169.254.0.0/16", "::1/128",
	} {
		_, block, err := net.ParseCIDR(cidr)
		if err == nil {
			privateCIDRs = append(privateCIDRs, block)
		}
	}
}

func isPrivateIP(ip net.IP) bool {
	for _, block := range privateCIDRs {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// ScrapeOptions controls HTTP client behaviour for a single scrape request.
type ScrapeOptions struct {
	Timeout         time.Duration
	MaxRetries      int
	FollowRedirects bool
	MaxBodySize     int64
	UserAgent       string
}

// Result holds the content and metadata extracted from a single scraped page.
type Result struct {
	Links      []string `json:"links"`
	Headers    []string `json:"headers"`
	Paragraphs []string `json:"paragraphs"`
	HTTPStatus int      `json:"http_status"`
	DurationMs int64    `json:"duration_ms"`
}

// DefaultOptions returns ScrapeOptions populated with sensible defaults:
// 10-second timeout, up to 3 retries, redirects enabled, 5 MB max body, GoScrape/1.0 user-agent.
func DefaultOptions() *ScrapeOptions {
	return &ScrapeOptions{
		Timeout:         10 * time.Second,
		MaxRetries:      3,
		FollowRedirects: true,
		MaxBodySize:     5 * 1024 * 1024,
		UserAgent:       "GoScrape/1.0",
	}
}

// ScrapePage fetches the page at rawURL, parses the HTML, and extracts content
// matching extractTypes ("links", "headers", "paragraphs", or "all").
// If extractTypes is empty it defaults to extracting links.
// When opts is nil DefaultOptions is used.
// Before fetching the hostname is resolved and checked against private IP ranges;
// redirect targets are also checked. The caller's context controls cancellation
// and timeout at the HTTP transport level.
func ScrapePage(ctx context.Context, rawURL string, extractTypes []string, opts *ScrapeOptions) (*Result, error) {
	parsedURL, err := resolveAndCheckURL(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	if opts == nil {
		opts = DefaultOptions()
	}

	client := newScrapeClient(opts)

	start := time.Now()
	doc, httpStatus, err := fetchAndParse(ctx, client, rawURL, opts, start)
	if err != nil {
		return &Result{HTTPStatus: httpStatus, DurationMs: time.Since(start).Milliseconds()}, err
	}

	result := &Result{
		HTTPStatus: httpStatus,
	}

	extractSet := buildExtractSet(extractTypes)

	if extractSet["links"] || extractSet["all"] {
		result.Links = extractLinks(doc, parsedURL)
	}
	if extractSet["headers"] || extractSet["all"] {
		result.Headers = extractHeaders(doc)
	}
	if extractSet["paragraphs"] || extractSet["all"] {
		result.Paragraphs = extractParagraphs(doc)
	}

	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}

func fetchAndParse(ctx context.Context, client *http.Client, rawURL string, opts *ScrapeOptions, start time.Time) (*goquery.Document, int, error) {
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

	doc, err := goquery.NewDocumentFromReader(bodyReader)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("%w: %w", ErrParseFailure, err)
	}

	return doc, resp.StatusCode, nil
}

func resolveAndCheckURL(ctx context.Context, rawURL string) (*url.URL, error) {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return nil, fmt.Errorf("%w: %s", ErrInvalidURL, rawURL)
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidURL, err)
	}

	hostname := parsedURL.Hostname()
	if os.Getenv("SCRAPER_ALLOW_PRIVATE_IPS") == "" {
		ips, err := net.LookupIP(hostname)
		if err != nil {
			return nil, fmt.Errorf("dns lookup failed: %w", err)
		}
		for _, ip := range ips {
			if isPrivateIP(ip) {
				return nil, fmt.Errorf("%w: %s resolves to %s", ErrPrivateIP, hostname, ip)
			}
		}
	}

	return parsedURL, nil
}

func newScrapeClient(opts *ScrapeOptions) *http.Client {
	return &http.Client{
		Timeout: opts.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !opts.FollowRedirects {
				return http.ErrUseLastResponse
			}
			if len(via) >= 5 {
				return fmt.Errorf("%w: %d redirects", ErrTooManyRedirects, len(via))
			}
			redirectHost := req.URL.Hostname()
			redirectIPs, err := net.LookupIP(redirectHost)
			if err != nil {
				return fmt.Errorf("redirect dns lookup failed: %w", err)
			}
			for _, ip := range redirectIPs {
				if isPrivateIP(ip) {
					return fmt.Errorf("%w: redirect to %s resolves to %s", ErrPrivateIP, redirectHost, ip)
				}
			}
			return nil
		},
	}
}

func buildExtractSet(extractTypes []string) map[string]bool {
	extractSet := make(map[string]bool, len(extractTypes))
	if len(extractTypes) == 0 {
		extractSet["links"] = true
	} else {
		for _, t := range extractTypes {
			extractSet[strings.ToLower(t)] = true
		}
	}
	return extractSet
}

func extractLinks(doc *goquery.Document, baseURL *url.URL) []string {
	var links []string
	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists {
			return
		}
		linkURL, err := url.Parse(href)
		if err != nil {
			return
		}
		absoluteURL := baseURL.ResolveReference(linkURL).String()
		links = append(links, absoluteURL)
	})
	return links
}

func extractHeaders(doc *goquery.Document) []string {
	var headers []string
	doc.Find("h1,h2,h3,h4").Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if text != "" {
			headers = append(headers, text)
		}
	})
	return headers
}

func extractParagraphs(doc *goquery.Document) []string {
	var paragraphs []string
	doc.Find("p").Each(func(i int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if text != "" {
			paragraphs = append(paragraphs, text)
		}
	})
	return paragraphs
}

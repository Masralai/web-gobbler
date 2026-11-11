// Package scraper provides the core web scraping engine.
// It fetches HTML pages over HTTP (or optional headless Chrome), parses the DOM with goquery,
// and extracts content. SSRF protection rejects private/loopback targets.
package scraper

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var (
	ErrInvalidURL       = errors.New("invalid URL: must be http:// or https://")
	ErrHTTPFailed       = errors.New("HTTP request failed with non-2xx status")
	ErrParseFailure     = errors.New("failed to parse response body")
	ErrTimeout          = errors.New("request timed out")
	ErrPrivateIP        = errors.New("request refused: target resolves to a private IP")
	ErrTooManyRedirects = errors.New("too many redirects")
)

var privateCIDRs []*net.IPNet

func init() {
	for _, cidr := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
		"169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16",
		"::1/128", "fc00::/7", "fe80::/10",
		"::ffff:0:0/96",
	} {
		_, block, err := net.ParseCIDR(cidr)
		if err == nil {
			privateCIDRs = append(privateCIDRs, block)
		}
	}
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, block := range privateCIDRs {
		if block.Contains(ip) {
			return true
		}
	}
	if v4 := ip.To4(); v4 != nil {
		for _, block := range privateCIDRs {
			if block.Contains(v4) {
				return true
			}
		}
	}
	return false
}

// ScrapeOptions controls fetch/parse behaviour for a single scrape request.
type ScrapeOptions struct {
	Timeout         time.Duration
	MaxRetries      int
	FollowRedirects bool
	MaxBodySize     int64
	UserAgent       string
	RenderJS        bool
}

// DefaultHTTPFetcher is used when RenderJS is false.
var DefaultHTTPFetcher Fetcher = HTTPFetcher{}

// DefaultBrowserFetcher is used when RenderJS is true.
var DefaultBrowserFetcher Fetcher = NewBrowserFetcher(2)

// PageResult is one page within a crawl/map job result.
type PageResult struct {
	URL        string   `json:"url"`
	Depth      int      `json:"depth"`
	Links      []string `json:"links,omitempty"`
	Headers    []string `json:"headers,omitempty"`
	Paragraphs []string `json:"paragraphs,omitempty"`
	Markdown   string   `json:"markdown,omitempty"`
	HTML       string   `json:"html,omitempty"`
	RawHTML    string   `json:"raw_html,omitempty"`
	HTTPStatus int      `json:"http_status,omitempty"`
	Truncated  bool     `json:"truncated,omitempty"`
}

// Result holds scrape/crawl/map output.
type Result struct {
	Links        []string     `json:"links,omitempty"`
	Headers      []string     `json:"headers,omitempty"`
	Paragraphs   []string     `json:"paragraphs,omitempty"`
	Markdown     string       `json:"markdown,omitempty"`
	HTML         string       `json:"html,omitempty"`
	RawHTML      string       `json:"raw_html,omitempty"`
	Truncated    bool         `json:"truncated,omitempty"`
	Pages        []PageResult `json:"pages,omitempty"`
	PagesCrawled int          `json:"pages_crawled,omitempty"`
	PagesSkipped int          `json:"pages_skipped,omitempty"`
	URLs         []string     `json:"urls,omitempty"`
	HTTPStatus   int          `json:"http_status"`
	ContentType  string       `json:"content_type,omitempty"`
	FinalURL     string       `json:"final_url,omitempty"`
	DurationMs   int64        `json:"duration_ms"`
}

// DefaultOptions returns sensible scrape defaults.
func DefaultOptions() *ScrapeOptions {
	return &ScrapeOptions{
		Timeout:         10 * time.Second,
		MaxRetries:      3,
		FollowRedirects: true,
		MaxBodySize:     5 * 1024 * 1024,
		UserAgent:       "GoScrape/1.0",
	}
}

// ScrapePage fetches and extracts content for extractTypes.
func ScrapePage(ctx context.Context, rawURL string, extractTypes []string, opts *ScrapeOptions) (*Result, error) {
	parsedURL, err := resolveAndCheckURL(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	if opts == nil {
		opts = DefaultOptions()
	}

	start := time.Now()
	var body []byte
	var httpStatus int
	var meta httpMeta
	if opts.RenderJS {
		var err error
		body, httpStatus, err = DefaultBrowserFetcher.Fetch(ctx, rawURL, opts)
		if err != nil {
			return &Result{HTTPStatus: httpStatus, DurationMs: time.Since(start).Milliseconds()}, err
		}
		meta = httpMeta{finalURL: rawURL}
	} else {
		var err error
		body, httpStatus, meta, err = fetchHTTP(ctx, rawURL, opts)
		if err != nil {
			return &Result{HTTPStatus: httpStatus, ContentType: meta.contentType, FinalURL: meta.finalURL, DurationMs: time.Since(start).Milliseconds()}, err
		}
	}
	doc, err := parseHTML(body)
	if err != nil {
		return &Result{HTTPStatus: httpStatus, ContentType: meta.contentType, FinalURL: meta.finalURL, DurationMs: time.Since(start).Milliseconds()}, err
	}

	result := &Result{HTTPStatus: httpStatus, ContentType: meta.contentType, FinalURL: meta.finalURL}
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
	if extractSet["markdown"] || extractSet["html"] || extractSet["all"] {
		main := mainContent(doc)
		clone := main.Clone()
		stripNoise(clone)
		if extractSet["markdown"] || extractSet["all"] {
			result.Markdown = htmlToMarkdown(clone)
		}
		if extractSet["html"] || extractSet["all"] {
			h, trunc := truncateHTML(selectionHTML(clone))
			result.HTML = h
			result.Truncated = result.Truncated || trunc
		}
	}
	if extractSet["raw_html"] {
		bodySel := doc.Find("body").First()
		if bodySel.Length() == 0 {
			bodySel = doc.Selection
		}
		h, trunc := truncateHTML(selectionHTML(bodySel))
		result.RawHTML = h
		result.Truncated = result.Truncated || trunc
	}

	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
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
	seen := make(map[string]bool)
	var links []string
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		linkURL, err := url.Parse(strings.TrimSpace(href))
		if err != nil {
			return
		}
		abs := baseURL.ResolveReference(linkURL)
		if abs.Scheme != "http" && abs.Scheme != "https" {
			return
		}
		u := abs.String()
		if !seen[u] {
			seen[u] = true
			links = append(links, u)
		}
	})
	return links
}

func extractHeaders(doc *goquery.Document) []string {
	var headers []string
	doc.Find("h1,h2,h3,h4").Each(func(i int, s *goquery.Selection) {
		if text := strings.TrimSpace(s.Text()); text != "" {
			headers = append(headers, text)
		}
	})
	return headers
}

func extractParagraphs(doc *goquery.Document) []string {
	var paragraphs []string
	doc.Find("p").Each(func(i int, s *goquery.Selection) {
		if text := strings.TrimSpace(s.Text()); text != "" {
			paragraphs = append(paragraphs, text)
		}
	})
	return paragraphs
}

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
	ErrInvalidURL    = errors.New("invalid URL: must be http:// or https://")
	ErrHTTPFailed    = errors.New("HTTP request failed with non-2xx status")
	ErrParseFailure  = errors.New("failed to parse response body")
	ErrTimeout       = errors.New("request timed out")
	ErrPrivateIP     = errors.New("request refused: target resolves to a private IP")
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

	if opts == nil {
		opts = DefaultOptions()
	}

	client := &http.Client{
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

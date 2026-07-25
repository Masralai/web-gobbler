// Package crawl implements same-origin BFS crawl and map discovery.
package crawl

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/Masralai/web-gobbler/internal/scraper"
)

// RateWaiter waits before fetching a hostname (per-domain rate limit).
type RateWaiter interface {
	Wait(ctx context.Context, hostname string) error
}

// Options controls crawl/map bounds.
type Options struct {
	MaxPages int
	MaxDepth int
	MaxURLs  int
	Extract  []string
	Scrape   scraper.ScrapeOptions
}

type frontierItem struct {
	URL   string
	Depth int
}

// NormalizeURL strips fragments and normalizes for dedupe.
func NormalizeURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Fragment = ""
	// drop default ports noise
	return u.String(), nil
}

func sameHost(a, b *url.URL) bool {
	return strings.EqualFold(a.Hostname(), b.Hostname())
}

// CrawlBFS fetches pages same-origin up to max pages/depth.
// rate may be nil. cancelCheck return true to stop early.
func CrawlBFS(ctx context.Context, seed string, opts Options, rate RateWaiter, cancelCheck func() bool) (*scraper.Result, error) {
	start := time.Now()
	if opts.MaxPages <= 0 {
		opts.MaxPages = 10
	}
	if opts.MaxDepth < 0 {
		opts.MaxDepth = 2
	}
	if len(opts.Extract) == 0 {
		opts.Extract = []string{"markdown"}
	}

	seedNorm, err := NormalizeURL(seed)
	if err != nil {
		return nil, err
	}
	seedURL, err := url.Parse(seedNorm)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{seedNorm: true}
	queue := []frontierItem{{URL: seedNorm, Depth: 0}}
	out := &scraper.Result{}
	var skipped int

	for len(queue) > 0 && len(out.Pages) < opts.MaxPages {
		if ctx.Err() != nil {
			break
		}
		if cancelCheck != nil && cancelCheck() {
			break
		}

		item := queue[0]
		queue = queue[1:]

		if rate != nil {
			if err := rate.Wait(ctx, seedURL.Hostname()); err != nil {
				return out, err
			}
		}

		page, err := scraper.ScrapePage(ctx, item.URL, opts.Extract, &opts.Scrape)
		pr := scraper.PageResult{
			URL:   item.URL,
			Depth: item.Depth,
		}
		if err != nil {
			skipped++
			if page != nil {
				pr.HTTPStatus = page.HTTPStatus
			}
			out.Pages = append(out.Pages, pr)
			continue
		}
		pr.Links = page.Links
		pr.Headers = page.Headers
		pr.Paragraphs = page.Paragraphs
		pr.Markdown = page.Markdown
		pr.HTML = page.HTML
		pr.RawHTML = page.RawHTML
		pr.HTTPStatus = page.HTTPStatus
		pr.Truncated = page.Truncated
		out.Pages = append(out.Pages, pr)
		out.Truncated = out.Truncated || page.Truncated

		if item.Depth >= opts.MaxDepth {
			continue
		}
		for _, link := range page.Links {
			norm, err := NormalizeURL(link)
			if err != nil {
				continue
			}
			u, err := url.Parse(norm)
			if err != nil || !sameHost(seedURL, u) {
				continue
			}
			if seen[norm] {
				continue
			}
			seen[norm] = true
			queue = append(queue, frontierItem{URL: norm, Depth: item.Depth + 1})
		}
	}

	out.PagesCrawled = len(out.Pages)
	out.PagesSkipped = skipped
	if len(out.Pages) > 0 {
		out.HTTPStatus = out.Pages[0].HTTPStatus
	}
	out.DurationMs = time.Since(start).Milliseconds()
	return out, nil
}

// MapBFS discovers same-origin URLs without full content extraction.
func MapBFS(ctx context.Context, seed string, opts Options, rate RateWaiter, cancelCheck func() bool) (*scraper.Result, error) {
	start := time.Now()
	if opts.MaxURLs <= 0 {
		opts.MaxURLs = 50
	}
	if opts.MaxDepth < 0 {
		opts.MaxDepth = 2
	}
	// light extract: links only
	opts.Extract = []string{"links"}

	seedNorm, err := NormalizeURL(seed)
	if err != nil {
		return nil, err
	}
	seedURL, err := url.Parse(seedNorm)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{seedNorm: true}
	urls := []string{seedNorm}
	queue := []frontierItem{{URL: seedNorm, Depth: 0}}

	for len(queue) > 0 && len(urls) < opts.MaxURLs {
		if ctx.Err() != nil {
			break
		}
		if cancelCheck != nil && cancelCheck() {
			break
		}

		item := queue[0]
		queue = queue[1:]
		if item.Depth >= opts.MaxDepth {
			continue
		}

		if rate != nil {
			if err := rate.Wait(ctx, seedURL.Hostname()); err != nil {
				return &scraper.Result{URLs: urls, DurationMs: time.Since(start).Milliseconds()}, err
			}
		}

		page, err := scraper.ScrapePage(ctx, item.URL, opts.Extract, &opts.Scrape)
		if err != nil {
			continue
		}
		for _, link := range page.Links {
			norm, err := NormalizeURL(link)
			if err != nil {
				continue
			}
			u, err := url.Parse(norm)
			if err != nil || !sameHost(seedURL, u) {
				continue
			}
			if seen[norm] {
				continue
			}
			seen[norm] = true
			urls = append(urls, norm)
			if len(urls) >= opts.MaxURLs {
				break
			}
			queue = append(queue, frontierItem{URL: norm, Depth: item.Depth + 1})
		}
	}

	return &scraper.Result{
		URLs:       urls,
		HTTPStatus: 200,
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

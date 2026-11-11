package crawl

import (
	"bufio"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Masralai/web-gobbler/internal/scraper"
)

type RobotsRules struct {
	Disallow []string
	Allow    []string
	Sitemaps []string
}

func FetchRobots(ctx context.Context, seed string, opts scraper.ScrapeOptions) (*RobotsRules, error) {
	u, err := url.Parse(seed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return &RobotsRules{}, nil
	}
	robotsURL := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/robots.txt"}).String()
	body, _, err := fetchText(ctx, robotsURL, opts, 512*1024)
	if err != nil {
		return &RobotsRules{}, nil
	}
	return parseRobots(string(body)), nil
}

func parseRobots(body string) *RobotsRules {
	r := &RobotsRules{}
	type group struct {
		agents   []string
		disallow []string
		allow    []string
	}
	var groups []group
	var cur *group
	flush := func() {
		if cur != nil && len(cur.agents) > 0 {
			groups = append(groups, *cur)
		}
		cur = nil
	}
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			flush()
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])
		switch key {
		case "user-agent":
			if cur == nil || len(cur.disallow) > 0 || len(cur.allow) > 0 {
				flush()
				cur = &group{}
			}
			cur.agents = append(cur.agents, strings.ToLower(val))
		case "disallow":
			if cur == nil {
				cur = &group{agents: []string{"*"}}
			}
			if val != "" {
				cur.disallow = append(cur.disallow, val)
			}
		case "allow":
			if cur == nil {
				cur = &group{agents: []string{"*"}}
			}
			if val != "" {
				cur.allow = append(cur.allow, val)
			}
		case "sitemap":
			if val != "" {
				r.Sitemaps = append(r.Sitemaps, val)
			}
		}
	}
	flush()

	pick := func(agent string) *group {
		agent = strings.ToLower(strings.TrimSpace(agent))
		if agent == "" {
			agent = "*"
		}
		for i := range groups {
			for _, a := range groups[i].agents {
				if a == agent {
					return &groups[i]
				}
			}
		}
		for i := range groups {
			for _, a := range groups[i].agents {
				if a == "*" {
					return &groups[i]
				}
			}
		}
		return nil
	}
	if g := pick(""); g != nil {
		r.Disallow = append(r.Disallow, g.disallow...)
		r.Allow = append(r.Allow, g.allow...)
	}
	// ponytail: simplified robots parsing/matching (single selected group, prefix/wildcard-lite).
	return r
}

func (r *RobotsRules) Allowed(rawURL string) bool {
	if r == nil || len(r.Disallow) == 0 {
		return true
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return true
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	bestAllow := -1
	bestDisallow := -1
	for _, p := range r.Allow {
		if robotsMatch(path, p) && len(p) > bestAllow {
			bestAllow = len(p)
		}
	}
	for _, p := range r.Disallow {
		if robotsMatch(path, p) && len(p) > bestDisallow {
			bestDisallow = len(p)
		}
	}
	if bestDisallow < 0 {
		return true
	}
	if bestAllow >= bestDisallow {
		return true
	}
	return false
}

func robotsMatch(path, pattern string) bool {
	if pattern == "" {
		return false
	}
	anchoredEnd := strings.HasSuffix(pattern, "$")
	p := pattern
	if anchoredEnd {
		p = strings.TrimSuffix(p, "$")
	}
	if !strings.Contains(p, "*") {
		if anchoredEnd {
			return path == p
		}
		return strings.HasPrefix(path, p)
	}
	parts := strings.Split(p, "*")
	idx := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		j := strings.Index(path[idx:], part)
		if j < 0 {
			return false
		}
		if i == 0 && !strings.HasPrefix(path, part) {
			return false
		}
		idx += j + len(part)
	}
	if anchoredEnd {
		last := parts[len(parts)-1]
		if last != "" {
			return strings.HasSuffix(path, last)
		}
	}
	return true
}

func DiscoverSitemapURLs(ctx context.Context, seed string, mode string, robots *RobotsRules, opts scraper.ScrapeOptions, limit int) ([]string, error) {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "skip" || mode == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 1000
	}
	u, err := url.Parse(seed)
	if err != nil {
		return nil, nil
	}
	starts := []string{(&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/sitemap.xml"}).String()}
	if robots != nil {
		for _, s := range robots.Sitemaps {
			if strings.TrimSpace(s) != "" {
				starts = append(starts, s)
			}
		}
	}
	seenSitemaps := map[string]bool{}
	seenURLs := map[string]bool{}
	var out []string
	var queue []string
	for _, s := range starts {
		if !seenSitemaps[s] {
			seenSitemaps[s] = true
			queue = append(queue, s)
		}
	}
	for len(queue) > 0 && len(out) < limit {
		sm := queue[0]
		queue = queue[1:]
		if err := scraper.ValidatePublicURL(ctx, sm); err != nil {
			continue
		}
		body, _, err := fetchText(ctx, sm, opts, 2*1024*1024)
		if err != nil || len(body) == 0 {
			continue
		}
		locs, nested, err := parseSitemapXML(body)
		if err != nil {
			continue
		}
		for _, loc := range locs {
			loc = strings.TrimSpace(loc)
			if loc == "" || seenURLs[loc] {
				continue
			}
			seenURLs[loc] = true
			out = append(out, loc)
			if len(out) >= limit {
				break
			}
		}
		for _, n := range nested {
			n = strings.TrimSpace(n)
			if n == "" || seenSitemaps[n] {
				continue
			}
			seenSitemaps[n] = true
			queue = append(queue, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

func parseSitemapXML(body []byte) (urls []string, sitemaps []string, err error) {
	decoder := xml.NewDecoder(strings.NewReader(string(body)))
	var stack []string
	var inLoc bool
	var inURL bool
	var inSitemap bool
	var locBuf strings.Builder
	for {
		tok, terr := decoder.Token()
		if terr != nil {
			if terr == io.EOF {
				break
			}
			return urls, sitemaps, terr
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			stack = append(stack, name)
			switch name {
			case "url":
				inURL = true
			case "sitemap":
				inSitemap = true
			case "loc":
				inLoc = true
				locBuf.Reset()
			}
		case xml.CharData:
			if inLoc {
				locBuf.Write([]byte(t))
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			if name == "loc" && inLoc {
				val := strings.TrimSpace(locBuf.String())
				if val != "" {
					if inSitemap {
						sitemaps = append(sitemaps, val)
					} else if inURL || len(stack) <= 2 {
						urls = append(urls, val)
					}
				}
				inLoc = false
			}
			if name == "url" {
				inURL = false
			}
			if name == "sitemap" {
				inSitemap = false
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return urls, sitemaps, nil
}

func fetchText(ctx context.Context, rawURL string, opts scraper.ScrapeOptions, maxBytes int64) ([]byte, int, error) {
	if maxBytes <= 0 {
		maxBytes = 1024 * 1024
	}
	client := &http.Client{Timeout: 10 * time.Second}
	if opts.Timeout > 0 {
		client.Timeout = opts.Timeout
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = "WebGobbler/2.0"
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/plain,application/xml,text/xml,*/*")
	for k, v := range opts.Headers {
		if k != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

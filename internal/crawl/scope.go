package crawl

import (
	"net/url"
	"path"
	"regexp"
	"strings"

	"golang.org/x/net/publicsuffix"
)

type Scope struct {
	Seed                  *url.URL
	AllowSubdomains       bool
	CrawlEntireDomain     bool
	AllowExternalLinks    bool
	IncludePaths          []string
	ExcludePaths          []string
	RegexOnFullURL        bool
	IgnoreQueryParameters bool
}

func SameHostname(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Hostname(), b.Hostname())
}

func SameRegistrableDomain(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	ah, errA := publicsuffix.EffectiveTLDPlusOne(strings.ToLower(a.Hostname()))
	bh, errB := publicsuffix.EffectiveTLDPlusOne(strings.ToLower(b.Hostname()))
	if errA != nil || errB != nil || ah == "" || bh == "" {
		return strings.EqualFold(a.Hostname(), b.Hostname())
	}
	return ah == bh
}

func NormalizeCrawlURL(raw string, ignoreQuery bool) (string, *url.URL, bool) {
	norm, err := NormalizeURL(raw)
	if err != nil {
		return "", nil, false
	}
	u, err := url.Parse(norm)
	if err != nil {
		return "", nil, false
	}
	if ignoreQuery {
		u.RawQuery = ""
		u.Fragment = ""
		norm = u.String()
	}
	return norm, u, true
}

func (s Scope) Allowed(raw string) bool {
	norm, u, ok := NormalizeCrawlURL(raw, s.IgnoreQueryParameters)
	if !ok {
		return false
	}
	if !s.hostAllowed(u) {
		return false
	}
	// REQ-002a1: path-scope check applies to internal/same-host links; for links that
	// fail the same-host test, host-scope governs (orthogonal to crawlEntireDomain path widening).
	if SameHostname(s.Seed, u) && !s.pathInScope(u) {
		return false
	}
	if !s.pathPatternAllowed(norm, u) {
		return false
	}
	return true
}

func (s Scope) hostAllowed(u *url.URL) bool {
	if SameHostname(s.Seed, u) {
		return true
	}
	if s.AllowExternalLinks {
		return true
	}
	if s.AllowSubdomains && SameRegistrableDomain(s.Seed, u) {
		return true
	}
	return false
}

func (s Scope) pathInScope(u *url.URL) bool {
	if s.Seed == nil || u == nil {
		return false
	}
	if s.CrawlEntireDomain {
		return true
	}
	seedPrefix := seedPathPrefix(s.Seed.EscapedPath())
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if p == seedPrefix || strings.HasPrefix(p, seedPrefix) {
		return true
	}
	// allow exact seed path when seed has no trailing slash and differs from prefix dir
	sp := s.Seed.EscapedPath()
	if sp == "" {
		sp = "/"
	}
	return p == sp
}

func seedPathPrefix(p string) string {
	if p == "" {
		return "/"
	}
	if strings.HasSuffix(p, "/") {
		return p
	}
	dir := path.Dir(p)
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	return dir
}

func (s Scope) pathPatternAllowed(norm string, u *url.URL) bool {
	target := norm
	if !s.RegexOnFullURL && u != nil {
		target = u.EscapedPath()
		if target == "" {
			target = "/"
		}
	}
	for _, p := range s.ExcludePaths {
		if re, err := regexp.Compile(p); err == nil && re.MatchString(target) {
			return false
		}
	}
	if len(s.IncludePaths) == 0 {
		return true
	}
	for _, p := range s.IncludePaths {
		if re, err := regexp.Compile(p); err == nil && re.MatchString(target) {
			return true
		}
	}
	return false
}

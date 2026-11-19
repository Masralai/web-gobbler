package scraper

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type pinnedTarget struct {
	host     string
	port     string
	addr     string
	hostname string
}

func resolvePublicTarget(ctx context.Context, rawURL string) (*url.URL, pinnedTarget, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, pinnedTarget{}, fmt.Errorf("%w: %s", ErrInvalidURL, rawURL)
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if os.Getenv("SCRAPER_ALLOW_PRIVATE_IPS") != "" {
		return parsed, pinnedTarget{host: host, port: port, addr: net.JoinHostPort(host, port), hostname: host}, nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, pinnedTarget{}, fmt.Errorf("dns lookup failed: %w", err)
	}
	for _, a := range ips {
		if isPrivateIP(a.IP) {
			return nil, pinnedTarget{}, fmt.Errorf("%w: %s resolves to %s", ErrPrivateIP, host, a.IP)
		}
	}
	ip := ips[0].IP
	return parsed, pinnedTarget{host: host, port: port, addr: net.JoinHostPort(ip.String(), port), hostname: host}, nil
}

func newPinnedHTTPClient(opts *ScrapeOptions, pinned pinnedTarget) *http.Client {
	dialer := &net.Dialer{Timeout: opts.Timeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			// Dial the pre-resolved, validated address.
			return dialer.DialContext(ctx, network, pinned.addr)
		},
		TLSClientConfig:       &tls.Config{ServerName: pinned.hostname},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   opts.Timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Redirects are handled manually by fetchHTTP to re-validate and re-pin each hop.
			return http.ErrUseLastResponse
		},
	}
}

func normalizeFinalURL(base *url.URL, location string) (string, error) {
	if strings.TrimSpace(location) == "" {
		return "", fmt.Errorf("redirect missing location")
	}
	ref, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

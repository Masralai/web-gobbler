package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type stubFetcher struct {
	html string
}

func (s stubFetcher) Fetch(ctx context.Context, rawURL string, opts *ScrapeOptions) ([]byte, int, error) {
	return []byte(s.html), 200, nil
}

func TestScrapePage_RenderJS_UsesBrowserFetcher(t *testing.T) {
	os.Setenv("SCRAPER_ALLOW_PRIVATE_IPS", "1")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><div id="app"></div></body></html>`))
	}))
	defer ts.Close()

	prev := DefaultBrowserFetcher
	DefaultBrowserFetcher = stubFetcher{html: `<html><body><main><h1>Rendered</h1><p>Hi</p></main></body></html>`}
	defer func() { DefaultBrowserFetcher = prev }()

	js, err := ScrapePage(context.Background(), ts.URL, []string{"markdown"}, &ScrapeOptions{
		Timeout:     DefaultOptions().Timeout,
		MaxBodySize: DefaultOptions().MaxBodySize,
		UserAgent:   "test",
		RenderJS:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js.Markdown, "Rendered") {
		t.Fatalf("expected rendered markdown, got %q", js.Markdown)
	}
}

func TestHTTPFetcher_OK(t *testing.T) {
	os.Setenv("SCRAPER_ALLOW_PRIVATE_IPS", "1")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><p>ok</p></body></html>`))
	}))
	defer ts.Close()
	body, status, err := HTTPFetcher{}.Fetch(context.Background(), ts.URL, DefaultOptions())
	if err != nil || status != 200 || !strings.Contains(string(body), "ok") {
		t.Fatalf("status=%d err=%v body=%s", status, err, body)
	}
}

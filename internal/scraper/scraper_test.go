package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	os.Setenv("SCRAPER_ALLOW_PRIVATE_IPS", "1")
	os.Exit(m.Run())
}

const testHTML = `<!DOCTYPE html>
<html>
<head><title>Test Page</title></head>
<body>
	<a href="/about">About</a>
	<a href="https://external.com">External</a>
	<a href="#section">Section</a>
	<a>No href</a>
	<h1>Main Title</h1>
	<h2>Section Title</h2>
	<h3>Sub Section</h3>
	<h4>Small Header</h4>
	<p>First paragraph content.</p>
	<p>Second paragraph with more text.</p>
	<div>Not a paragraph</div>
</body>
</html>`

func TestScrapePage_Links(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testHTML))
	}))
	defer ts.Close()

	result, err := ScrapePage(context.Background(), ts.URL, []string{"links"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.HTTPStatus != http.StatusOK {
		t.Errorf("expected HTTP 200, got %d", result.HTTPStatus)
	}

	if len(result.Links) != 3 {
		t.Errorf("expected 3 links (no-href excluded), got %d", len(result.Links))
	}

	if result.DurationMs < 0 {
		t.Errorf("expected non-negative DurationMs, got %d", result.DurationMs)
	}
}

func TestScrapePage_Headers(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testHTML))
	}))
	defer ts.Close()

	result, err := ScrapePage(context.Background(), ts.URL, []string{"headers"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Headers) != 4 {
		t.Errorf("expected 4 headers, got %d: %v", len(result.Headers), result.Headers)
	}
	if result.Headers[0] != "Main Title" {
		t.Errorf("expected 'Main Title', got '%s'", result.Headers[0])
	}
}

func TestScrapePage_Paragraphs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testHTML))
	}))
	defer ts.Close()

	result, err := ScrapePage(context.Background(), ts.URL, []string{"paragraphs"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Paragraphs) != 2 {
		t.Errorf("expected 2 paragraphs, got %d", len(result.Paragraphs))
	}
}

func TestScrapePage_All(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testHTML))
	}))
	defer ts.Close()

	result, err := ScrapePage(context.Background(), ts.URL, []string{"all"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Links) != 3 {
		t.Errorf("expected 3 links, got %d", len(result.Links))
	}
	if len(result.Headers) != 4 {
		t.Errorf("expected 4 headers, got %d", len(result.Headers))
	}
	if len(result.Paragraphs) != 2 {
		t.Errorf("expected 2 paragraphs, got %d", len(result.Paragraphs))
	}
	if result.Markdown == "" || !strings.Contains(result.Markdown, "Main Title") {
		t.Errorf("expected markdown with title, got %q", result.Markdown)
	}
	if result.HTML == "" {
		t.Errorf("expected html for extract all")
	}
}

func TestScrapePage_MarkdownAndHTML(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html><body><main><h1>Hi</h1><p>Body</p></main></body></html>`))
	}))
	defer ts.Close()

	result, err := ScrapePage(context.Background(), ts.URL, []string{"markdown", "html", "links"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Markdown, "# Hi") {
		t.Errorf("markdown: %q", result.Markdown)
	}
	if !strings.Contains(result.HTML, "Hi") {
		t.Errorf("html: %q", result.HTML)
	}
}

func TestScrapePage_EmptyExtractDefaultsToLinks(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testHTML))
	}))
	defer ts.Close()

	result, err := ScrapePage(context.Background(), ts.URL, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Links) == 0 {
		t.Errorf("expected links by default when extractTypes is empty")
	}
}

func TestScrapePage_RelativeURLResolution(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<a href="/relative">Relative</a>`))
	}))
	defer ts.Close()

	result, err := ScrapePage(context.Background(), ts.URL, []string{"links"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(result.Links))
	}
	expected := ts.URL + "/relative"
	if result.Links[0] != expected {
		t.Errorf("expected '%s', got '%s'", expected, result.Links[0])
	}
}

func TestScrapePage_InvalidURL(t *testing.T) {
	_, err := ScrapePage(context.Background(), "ftp://bad", []string{"links"}, nil)
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestScrapePage_NoProtocolURL(t *testing.T) {
	_, err := ScrapePage(context.Background(), "example.com", []string{"links"}, nil)
	if err == nil {
		t.Fatal("expected error for URL without protocol")
	}
}

func TestScrapePage_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ctx := context.Background()
	_, err := ScrapePage(ctx, ts.URL, []string{"links"}, &ScrapeOptions{Timeout: 1 * time.Millisecond})
	if err == nil {
		t.Fatal("expected error for timeout")
	}
}

func TestScrapePage_Non200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	result, err := ScrapePage(context.Background(), ts.URL, []string{"links"}, nil)
	if err == nil {
		t.Fatal("expected error for non-200")
	}
	if result == nil || result.HTTPStatus != http.StatusForbidden {
		t.Errorf("expected HTTP 403 in result, got %v", result)
	}
}

func TestScrapePage_CancelledContext(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ScrapePage(ctx, ts.URL, []string{"links"}, nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestScrapePage_NoFollowRedirects(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/other", http.StatusMovedPermanently)
	}))
	defer ts.Close()

	result, err := ScrapePage(context.Background(), ts.URL, []string{"links"}, &ScrapeOptions{
		FollowRedirects: false,
	})
	if err == nil {
		t.Fatal("expected error for redirect with FollowRedirects=false")
	}
	if result == nil {
		t.Fatal("expected non-nil result even on error")
	}
	if result.HTTPStatus != http.StatusMovedPermanently {
		t.Errorf("expected HTTP 301, got %d", result.HTTPStatus)
	}
}

func TestScrapePage_NilOptions(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testHTML))
	}))
	defer ts.Close()

	result, err := ScrapePage(context.Background(), ts.URL, []string{"links"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HTTPStatus != http.StatusOK {
		t.Errorf("expected HTTP 200, got %d", result.HTTPStatus)
	}
}

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()
	if opts.Timeout != 10*time.Second {
		t.Errorf("expected 10s timeout, got %v", opts.Timeout)
	}
	if opts.MaxBodySize != 5*1024*1024 {
		t.Errorf("expected 5MB max body, got %d", opts.MaxBodySize)
	}
	if !opts.FollowRedirects {
		t.Errorf("expected FollowRedirects to be true")
	}
}

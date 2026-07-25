package scraper

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestHtmlToMarkdown_Basic(t *testing.T) {
	html := `<article>
		<nav>skip</nav>
		<h1>Title</h1>
		<p>Hello <a href="/x">world</a>.</p>
		<ul><li>One</li><li>Two</li></ul>
		<script>evil()</script>
	</article>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	main := mainContent(doc)
	stripNoise(main)
	md := htmlToMarkdown(main)
	if !strings.Contains(md, "# Title") {
		t.Fatalf("missing heading: %q", md)
	}
	if !strings.Contains(md, "[world](/x)") {
		t.Fatalf("missing link: %q", md)
	}
	if !strings.Contains(md, "- One") {
		t.Fatalf("missing list: %q", md)
	}
	if strings.Contains(md, "evil") || strings.Contains(md, "skip") {
		t.Fatalf("noise not stripped: %q", md)
	}
}

func TestHtmlToMarkdown_EmptyBody(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`<html><body></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	md := htmlToMarkdown(mainContent(doc))
	if md != "" {
		t.Fatalf("expected empty, got %q", md)
	}
}

func TestTruncateHTML(t *testing.T) {
	s := strings.Repeat("a", maxStoredHTML+10)
	out, trunc := truncateHTML(s)
	if !trunc || len(out) != maxStoredHTML {
		t.Fatalf("trunc=%v len=%d", trunc, len(out))
	}
}

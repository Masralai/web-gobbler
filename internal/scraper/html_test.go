package scraper

import "testing"

func TestMarkdownFromHTML(t *testing.T) {
	got, err := MarkdownFromHTML([]byte(`<html><body><article><h1>Title</h1><p>Hello <a href="/x">link</a></p></article></body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("empty markdown")
	}
}

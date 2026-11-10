package v2

import (
	"testing"
)

func TestFormatsUnmarshal(t *testing.T) {
	var f Formats
	if err := f.UnmarshalJSON([]byte(`["markdown",{"type":"json","prompt":"name"}]`)); err != nil {
		t.Fatal(err)
	}
	if len(f) != 2 || f[0].Type != "markdown" || f[1].Type != "json" || f[1].Prompt != "name" {
		t.Fatalf("unexpected formats: %#v", f)
	}
}

func TestStrictUnmarshalRejectsUnknown(t *testing.T) {
	var req ScrapeRequest
	err := strictUnmarshal([]byte(`{"url":"https://example.com","nope":true}`), &req)
	if err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestValidateScrapeRequestDefaultsAndBounds(t *testing.T) {
	req := ScrapeRequest{URL: "https://example.com"}
	if err := validateScrapeRequest(&req, false); err != nil {
		t.Fatal(err)
	}
	if req.Timeout != 30000 || len(req.Formats) != 1 || req.Formats[0].Type != "markdown" {
		t.Fatalf("defaults not applied: %#v", req)
	}

	req = ScrapeRequest{URL: "https://example.com", ScrapeOptions: ScrapeOptions{Timeout: 30000, WaitFor: 20000}}
	if err := validateScrapeRequest(&req, false); err == nil {
		t.Fatal("expected waitFor bound error")
	}

	req = ScrapeRequest{URL: "https://example.com", ScrapeOptions: ScrapeOptions{Formats: Formats{{Type: "nope"}}}}
	if err := validateScrapeRequest(&req, false); err == nil {
		t.Fatal("expected unsupported format error")
	}
}

func TestWireStatus(t *testing.T) {
	cases := map[string]string{
		"queued": "scraping", "processing": "scraping",
		"completed": "completed", "failed": "failed", "cancelled": "cancelled",
	}
	for in, want := range cases {
		if got := wireStatus(in); got != want {
			t.Fatalf("wireStatus(%q)=%q want %q", in, got, want)
		}
	}
}

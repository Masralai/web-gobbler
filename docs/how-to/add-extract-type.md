# How to add a new extract type

This guide walks through adding a new content extraction type (for example, extracting all `<img>` tags or `<code>` blocks) to GoScrape.

## Overview

Extract types are defined in three places:

1. **The scraper** (`internal/scraper/scraper.go`) — the `ScrapePage` function parses the HTML and collects matching elements.
2. **The result type** — the `Result` struct stores the extracted data (`links`, `headers`, `paragraphs`, `markdown`, `html`, `raw_html`, plus crawl/map aggregates).
3. **The validator** (`internal/api/validator.go`) — validates that the extract type string is recognised by the API.

Built-in types: `links`, `headers`, `paragraphs`, `markdown`, `html`, `raw_html`.

## Example: Add an `images` extract type

We will add support for extracting `<img src="...">` attributes from scraped pages.

### Step 1 — Add a field to the Result struct

Edit `internal/scraper/scraper.go`:

```go
type Result struct {
	Links      []string `json:"links"`
	Headers    []string `json:"headers"`
	Paragraphs []string `json:"paragraphs"`
	Images     []string `json:"images"`      // <-- new field
	HTTPStatus int      `json:"http_status"`
	DurationMs int64    `json:"duration_ms"`
}
```

### Step 2 — Add the extraction logic

Inside `ScrapePage`, after the paragraphs block, add:

```go
if extractSet["images"] || extractSet["all"] {
	doc.Find("img").Each(func(i int, s *goquery.Selection) {
		src, exists := s.Attr("src")
		if !exists {
			return
		}
		imgURL, err := url.Parse(src)
		if err != nil {
			return
		}
		absoluteURL := parsedURL.ResolveReference(imgURL).String()
		result.Images = append(result.Images, absoluteURL)
	})
}
```

> [!TIP]
> The existing `extractSet["all"]` branch already covers this — any extract type you add is automatically included when a client sends `"extract": ["all"]`.

### Step 3 — Update the validator

Edit `internal/api/validator.go` and add `"images"` to the allowed list:

```go
var validExtractTypes = map[string]bool{
	"links":      true,
	"headers":    true,
	"paragraphs": true,
	"images":     true,
	"all":        true,
}
```

### Step 4 — Update the job response building

Edit `internal/api/handlers.go` in the `jobToResponse` function. The `JobMeta` struct already computes counts from the result fields — add the new count:

```go
case store.JobStatusCompleted:
	resp.Results = job.Result
	if job.Result != nil {
		resp.Meta = &JobMeta{
			LinksCount:      len(job.Result.Links),
			HeadersCount:    len(job.Result.Headers),
			ParagraphsCount: len(job.Result.Paragraphs),
			ImagesCount:     len(job.Result.Images),  // <-- new field
			HTTPStatus:      job.Result.HTTPStatus,
			RetriesUsed:     job.RetriesUsed,
		}
	}
```

You must also add the `ImagesCount` field to the `JobMeta` struct in `internal/api/types.go`:

```go
type JobMeta struct {
	LinksCount      int `json:"links_count"`
	HeadersCount    int `json:"headers_count"`
	ParagraphsCount int `json:"paragraphs_count"`
	ImagesCount     int `json:"images_count"`    // <-- new field
	HTTPStatus      int `json:"http_status"`
	RetriesUsed     int `json:"retries_used"`
}
```

### Step 5 — Add unit tests

Add a test case in `internal/scraper/scraper_test.go`:

```go
func TestScrapePage_Images(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html><body><img src="/logo.png"><img src="https://example.com/banner.jpg"></body></html>`))
	}))
	defer ts.Close()

	result, err := ScrapePage(context.Background(), ts.URL, []string{"images"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Images) != 2 {
		t.Errorf("expected 2 images, got %d", len(result.Images))
	}
}
```

### Step 6 — Build and test

```bash
SCRAPER_ALLOW_PRIVATE_IPS=1 go test -race -count=1 ./internal/scraper/...
SCRAPER_ALLOW_PRIVATE_IPS=1 go test -race -count=1 ./internal/api/...
```

### Step 7 — Update the API docs

If you maintain the README API documentation, add `"images"` to the list of extract types mentioned in the `POST /scrape` example.

## Summary of all files changed

| File | Change |
|------|--------|
| `internal/scraper/scraper.go` | Added `Images` field to `Result`, added extraction logic |
| `internal/api/types.go` | Added `ImagesCount` field to `JobMeta` |
| `internal/api/handlers.go` | Added `ImagesCount` computation in `jobToResponse` |
| `internal/api/validator.go` | Added `"images"` to `validExtractTypes` |
| `internal/scraper/scraper_test.go` | Added `TestScrapePage_Images` |

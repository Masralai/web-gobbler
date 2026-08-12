# How to crawl and map a site

Same-origin multi-page jobs on the existing async API. **robots.txt is not consulted** (documented choice for P2/P3).

## Crawl (content)

```bash
curl -X POST http://localhost:8080/api/v1/crawl \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://example.com",
    "extract": ["markdown", "links"],
    "options": { "max_pages": 10, "max_depth": 2, "timeout_seconds": 15 }
  }'
```

Poll `poll_url`. Completed jobs have:

```json
{
  "results": {
    "pages": [
      { "url": "https://example.com/", "depth": 0, "markdown": "# ...", "http_status": 200 }
    ],
    "pages_crawled": 1,
    "pages_skipped": 0
  }
}
```

- Only links on the **same host** are followed.
- Fragments (`#...`) are stripped for dedupe.
- Cancel via `DELETE /api/v1/jobs/:id` while queued, or mid-crawl once processing (worker stops enqueueing further pages when status is no longer `processing`).

## Map (URLs only)

```bash
curl -X POST http://localhost:8080/api/v1/map \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://example.com",
    "options": { "max_urls": 50, "max_depth": 2 }
  }'
```

Completed `results.urls` lists discovered URLs (no markdown bodies).

## Limits

| Option | Default | Max |
|--------|---------|-----|
| `max_pages` (crawl) | 10 | 100 |
| `max_depth` | 2 | 5 |
| `max_urls` (map) | 50 | 500 |

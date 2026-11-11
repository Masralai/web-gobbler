---
name: web-gobbler
description: Use a self-hosted Web Gobbler instance as a v2 compatible scraper. Use when an app needs scrape, crawl, map, search, batch scrape, extract, or parse against WEB_GOBBLER_URL.
license: MIT
metadata:
  author: web-gobbler
  version: "1.0.0"
inputs:
  - name: WEB_GOBBLER_URL
    description: Base URL of the Web Gobbler instance, for example http://localhost:8080
    required: true
  - name: WEB_GOBBLER_API_KEY
    description: Bearer key when the deployment sets API_KEY
    required: false
---

# Web Gobbler

Self-hosted web scraping service with a `/v2` HTTP API. Point your client at `WEB_GOBBLER_URL` and call the endpoints directly.

## Quick start

```bash
curl -s -X POST "$WEB_GOBBLER_URL/v2/scrape" \
  -H "Authorization: Bearer $WEB_GOBBLER_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","formats":["markdown","links"]}'
```

## Endpoint map

- `POST /v2/scrape` — synchronous single page. Returns `data.markdown`, `data.links`, `data.metadata`.
- `GET /v2/scrape/:id` — scrape status.
- `POST /v2/crawl` — async crawl. Returns `id` and absolute `url`. Poll `GET /v2/crawl/:id`.
- `POST /v2/map` — synchronous URL discovery.
- `POST /v2/batch/scrape` — async list of URLs.
- `POST /v2/search` — SearXNG-backed web search, optional `scrapeOptions`.
- `POST /v2/extract` — async schema/prompt extract. Requires a configured LLM on self-host.
- `POST /v2/parse` — multipart file upload. PDF, DOCX, HTML, MD, TXT.
- `GET /v2/concurrency-check` — in-flight count.

## Rules

- All responses use `{ "success": true|false }`. Failures include `error` and usually `code`.
- Unknown request fields return `400`. Send only documented fields.
- Crawl and batch `url` and `next` are absolute URLs.
- `x-idempotency-key` is accepted on `POST /v2/crawl`; reuse within 24 hours returns `409`.
- `/v2/scrape/:id/interact` returns `501` by design.
- `creditsUsed` is always `0`.
- The hosted demo disables extract; self-host sets `LLM_API_KEY`.

## Verification

Run one scrape and assert `success: true`, non-empty `data.markdown`, and `data.metadata.statusCode`. Then run a crawl with `limit: 2` and poll until `status` is `completed` or `failed`.

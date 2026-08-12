# How to enable JS rendering (P4)

Static HTTP fetch fails on pages whose body is empty until JavaScript runs (many SPAs).

## Evidence (why this exists)

| Target class | Static scrape | With `render_js` |
|--------------|---------------|------------------|
| Empty `#app` shell + client render | Little/no markdown | DOM after JS |
| Docs / marketing HTML | Works | Unnecessary cost |

Do **not** enable browser by default — memory and latency are much higher.

## Local: compose profile

Default `worker` image stays distroless (no Chrome). Browser worker:

```bash
docker compose --profile browser up -d --build
```

Uses [`docker/Dockerfile.worker-browser`](../../docker/Dockerfile.worker-browser) (`chromedp/headless-shell`) with `RENDER_JS_ENABLED=1`.

You can run **either** the normal worker **or** `worker-browser` (or both sharing the queue — only jobs with `render_js: true` use Chrome).

## API

```bash
curl -X POST http://localhost:8080/api/v1/scrape \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://example.com",
    "extract": ["markdown"],
    "options": { "render_js": true, "timeout_seconds": 30 }
  }'
```

If Chrome is unavailable, the job fails with a clear browser-unavailable error.

## Ops notes

- Concurrency: browser fetcher caps parallel Chrome sessions (default 2); set `WORKER_CONCURRENCY` low (2).
- Memory: budget ~1–2 GB for the browser worker.
- SSRF: URL still resolved/checked before navigate.
- Distroless `Dockerfile.worker` must **not** ship Chromium.

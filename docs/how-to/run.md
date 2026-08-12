# How to run Web Gobbler

Start the stack, scrape a page, and optionally enable crawl, JS render, or LLM extract.

## Prerequisites

- Docker + Docker Compose v2
- Free ports: **8080**, **5432**, **6379**, **9090**, **3000**
- (Optional, for Go tests) Go **1.26+**

## Quick start

```bash
git clone https://github.com/Masralai/web-gobbler
cd web-gobbler
docker compose up -d --build
docker compose ps
curl -s http://localhost:8080/health
curl -s http://localhost:8080/api/v1/health
```

Healthy response:

```json
{"status":"ok","db":"ok","redis":"ok","version":"1.0.0"}
```

Schema is applied automatically on API/worker boot (`store.Migrate`). You do not need a separate migrate step.

### Services

| Service | Port | Role |
|---------|------|------|
| `api` | 8080 | REST API + `/metrics` |
| `worker` | — | Scrapes jobs from Redis |
| `postgres` | 5432 | Job storage |
| `redis` | 6379 | Job queue |
| `prometheus` | 9090 | Metrics |
| `grafana` | 3000 | Dashboards (anonymous auth on) |

Default DB URL used in Compose: `postgresql://user:pass@postgres:5432/goscrape`.

## First scrape (markdown)

```bash
curl -s -X POST http://localhost:8080/api/v1/scrape \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","extract":["markdown","links"]}'
```

Copy `job_id` / `poll_url`, then:

```bash
curl -s http://localhost:8080/api/v1/jobs/<job_id>
```

Poll until `status` is `completed` or `failed`. Completed jobs include `results.markdown`.

### Other extract types

`links`, `headers`, `paragraphs`, `markdown`, `html`, `raw_html` — combine in one request, e.g. `["markdown","html","links"]`.

## Crawl and map

```bash
# multi-page same-origin crawl
curl -s -X POST http://localhost:8080/api/v1/crawl \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","extract":["markdown"],"options":{"max_pages":10,"max_depth":2}}'

# URL discovery only
curl -s -X POST http://localhost:8080/api/v1/map \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","options":{"max_urls":50,"max_depth":2}}'
```

Details: [crawl.md](crawl.md).

## Optional: JS rendering (P4)

Default worker image has **no** Chrome. For `render_js`:

```bash
docker compose --profile browser up -d --build
```

```bash
curl -s -X POST http://localhost:8080/api/v1/scrape \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","extract":["markdown"],"options":{"render_js":true,"timeout_seconds":30}}'
```

Details: [browser-render.md](browser-render.md).

## Optional: LLM extract (P5)

Set on the **api** service (e.g. in `docker-compose.yml` or an `.env` file Compose reads):

```bash
LLM_API_KEY=sk-...
# optional:
# LLM_BASE_URL=https://api.openai.com/v1
# LLM_MODEL=gpt-4o-mini
```

Then restart API and, after a markdown job completes:

```bash
curl -s -X POST http://localhost:8080/api/v1/jobs/<job_id>/extract \
  -H "Content-Type: application/json" \
  -d '{"prompt":"Extract the page title","schema":{"type":"object","properties":{"title":{"type":"string"}}}}'
```

Without a key, this endpoint returns **501**. Details: [llm-extract.md](llm-extract.md).

## Useful endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/health` or `/api/v1/health` | Liveness / DB+Redis |
| POST | `/api/v1/scrape` | Single-page job |
| POST | `/api/v1/crawl` | Multi-page job |
| POST | `/api/v1/map` | URL discovery |
| GET | `/api/v1/jobs/:id` | Poll results |
| GET | `/api/v1/jobs` | List jobs |
| DELETE | `/api/v1/jobs/:id` | Cancel queued job |
| POST | `/api/v1/jobs/:id/extract` | Optional LLM extract |
| GET | `/metrics` | Prometheus |

Grafana: http://localhost:3000 — see [grafana-dashboard.md](grafana-dashboard.md).

## Smoke test everything

With the stack up:

```bash
./scripts/feature-matrix.sh http://localhost:8080
```

Expect 12 passes (health, markdown, html, crawl, map, cancel/list, extract 501, render_js accepted, metrics).

## Run without Docker (API + worker locally)

```bash
# terminals: postgres + redis via compose only
docker compose up -d postgres redis

export DATABASE_URL='postgresql://user:pass@localhost:5432/goscrape'
export REDIS_URL='redis://localhost:6379'
export LOG_LEVEL=INFO

go run ./cmd/api
# other terminal:
go run ./cmd/worker
```

## Tests

```bash
go test ./internal/...
go test -tags=integration ./test/integration/...
```

## Tear down

```bash
docker compose --profile browser down
# remove volumes too:
docker compose down -v
```

## Next reading

- Tutorial: [../tutorials/getting-started.md](../tutorials/getting-started.md)
- Product roadmap / phase log: [../../spec.md](../../spec.md)
- AWS deploy: [deploy-aws.md](deploy-aws.md)

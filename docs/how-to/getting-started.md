# Getting started with Web Gobbler

In this tutorial you will start Web Gobbler locally, scrape a page to markdown, then run optional LLM extract on that job — end to end on your machine.

For crawl, map, JS render, and the feature matrix, see **[How to run](run.md)**.

## What you will learn

- How to start the Web Gobbler stack with Docker Compose
- How to submit a scraping job and poll until it completes
- How to read LLM-ready markdown from the job result
- How to enable and call optional LLM extract (`POST /jobs/:id/extract`)
- How to tear the stack down

## Prerequisites

- **Docker and Docker Compose** — verify with `docker compose version`
- Ports **8080**, **5432**, **6379**, **9090**, and **3000** free on your host
- (Optional, for LLM extract) A **Gemini API key** (default) — or any OpenAI-compatible provider if you override base URL/model
- (Optional, for running Go tests later) **Go 1.26+**

> [!NOTE]
> If you use macOS you can install Docker Desktop from [docker.com](https://www.docker.com/products/docker-desktop). On Linux, install the `docker-compose-plugin` package for your distribution.

## Step 1 — Start the stack

Clone the repository and start all services:

```bash
git clone https://github.com/Masralai/web-gobbler
cd web-gobbler
docker compose up -d --build
```

Docker Compose starts six services:

| Service      | Purpose                     | Port |
|--------------|-----------------------------|------|
| `postgres`   | Job storage (PostgreSQL 16) | 5432 |
| `redis`      | Job queue (Redis 7)         | 6379 |
| `api`        | REST API (Gin)              | 8080 |
| `worker`     | Background scraper pool     | —    |
| `prometheus` | Metrics collection          | 9090 |
| `grafana`    | Metrics dashboard           | 3000 |

> [!NOTE]
> For AWS-shaped local practice (Terraform → Floci), use `./scripts/floci-up.sh` instead — see [How to run](run.md#ways-to-run). Do not run Compose `api` and Floci ECS on port **8080** at the same time.

Wait for services to become healthy:

```bash
docker compose ps
```

Every service should show `Up` and `(healthy)`. This usually takes 5–10 seconds.

## Step 2 — Verify the API is running

```bash
curl -s http://localhost:8080/api/v1/health
```

You should see:

```json
{"status":"ok","db":"ok","redis":"ok","version":"1.0.0"}
```

> [!TIP]
> If you see `"status":"degraded"` wait a few seconds and try again. The API can start before PostgreSQL and Redis finish initialising.

## Step 3 — Submit a scraping job

Scrape [example.com](https://example.com) for markdown (and links):

```bash
curl -s -X POST http://localhost:8080/api/v1/scrape \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://example.com",
    "extract": ["markdown", "links"],
    "options": {
      "timeout_seconds": 15,
      "max_retries": 2
    }
  }'
```

The API responds with `202 Accepted`:

```json
{
  "job_id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "status": "queued",
  "created_at": "2025-09-01T10:32:00Z",
  "poll_url": "/api/v1/jobs/f47ac10b-58cc-4372-a567-0e02b2c3d479"
}
```

Copy the `job_id` — you need it for polling and for LLM extract.

## Step 4 — Poll until the job completes

```bash
JOB_ID=f47ac10b-58cc-4372-a567-0e02b2c3d479   # replace with yours
curl -s "http://localhost:8080/api/v1/jobs/$JOB_ID"
```

While the job is running you may see `"status":"queued"` or `"processing"`. Poll every 2–3 seconds until `"status":"completed"` (or `"failed"`).

When complete, the response includes markdown (and links if you requested them):

```json
{
  "job_id": "f47ac10b-...",
  "status": "completed",
  "url": "https://example.com",
  "created_at": "2025-09-01T10:32:00Z",
  "completed_at": "2025-09-01T10:32:03Z",
  "duration_ms": 1240,
  "results": {
    "markdown": "# Example Domain\n\nThis domain is for use in illustrative examples...",
    "links": ["https://www.iana.org/domains/example"]
  },
  "meta": {
    "links_count": 1,
    "http_status": 200,
    "retries_used": 0
  }
}
```

Confirm `results.markdown` is present — LLM extract in the next steps needs it.

> [!TIP]
> Most jobs finish in 1–2 seconds. Increase `timeout_seconds` for slow sites. Other extract types (`headers`, `paragraphs`, `html`, `raw_html`) are documented in [How to run](run.md).

## Step 5 — Enable LLM extract

Optional **one-shot** structured extract over the markdown you already scraped. Not an agent (no tools, browse, or search). Without a key, `POST /jobs/:id/extract` returns **501**.

Set the key on the **api** service via a project `.env` file (Compose reads it automatically) or your shell:

```bash
# in the repo root — .env is gitignored if you add one, or export in the shell
export LLM_API_KEY=...   # Gemini key from Google AI Studio
# defaults (Gemini free-tier friendly):
# LLM_BASE_URL=https://generativelanguage.googleapis.com/v1beta/openai
# LLM_MODEL=gemini-2.5-flash
# override both for OpenAI or a local OpenAI-compatible server
```

Recreate the API so it picks up the env:

```bash
docker compose up -d --force-recreate api
```

Check logs for something like `LLM extract enabled`:

```bash
docker compose logs api | tail -20
```

Details and crawl-per-page behavior: [LLM extract](llm-extract.md).

## Step 6 — Run LLM extract on the job

Use the same completed `JOB_ID` from Step 4:

```bash
curl -s -X POST "http://localhost:8080/api/v1/jobs/$JOB_ID/extract" \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "Extract the page title",
    "schema": {
      "type": "object",
      "properties": { "title": { "type": "string" } },
      "required": ["title"]
    }
  }'
```

Successful response:

```json
{
  "job_id": "f47ac10b-...",
  "extracted": { "title": "Example Domain" }
}
```

The scrape job’s markdown in the database is unchanged. If extract fails, the original job result remains.

## Optional — Grafana and tests

Open [http://localhost:3000](http://localhost:3000) (admin / admin) for the bundled dashboard — see [grafana-dashboard.md](grafana-dashboard.md).

To run tests (needs Go 1.26+):

```bash
go test -race -cover ./...
docker compose up -d postgres redis
go test -tags=integration -race -v ./test/integration/...
```

Smoke more of the API: `./scripts/feature-matrix.sh http://localhost:8080` — see [How to run](run.md#smoke-test-everything).

## Step 7 — Tear down

```bash
docker compose down -v
```

The `-v` flag removes named volumes (Postgres/Redis data). Omit `-v` to keep data for the next session.

## What you have accomplished

- Started the full Web Gobbler stack locally
- Submitted an asynchronous scrape and polled until completion
- Retrieved LLM-ready markdown from the job result
- Enabled and called optional one-shot LLM extract
- Torn the stack down cleanly

Next: [How to run](run.md) for crawl, map, browser render, and the feature matrix; [deploy-aws.md](deploy-aws.md) for production.

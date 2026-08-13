# Getting started with Web Gobbler

In this tutorial you will submit your first scraping job, track its progress, and retrieve the extracted content — all on your local machine.

For the full operator runbook (crawl, map, browser, LLM extract, feature matrix), see **[How to run](../how-to/run.md)**.

## What you will learn

- How to start the Web Gobbler stack with Docker Compose
- How to submit a scraping job via the REST API
- How to poll for results until the job completes
- How to view scraped content (links, headers, paragraphs)
- How to run the test suite
- How to tear everything down

## Prerequisites

- **Go 1.26+** — verify with `go version`
- **Docker and Docker Compose** — verify with `docker compose version`
- Ports 8080, 5432, 6379, 9090, and 3000 free on your host

> [!NOTE]
> If you use macOS you can install Docker Desktop from [docker.com](https://www.docker.com/products/docker-desktop). On Linux, install the `docker-compose-plugin` package for your distribution.

## Step 1 — Start the stack

Clone the repository and start all services:

```bash
git clone https://github.com/Masralai/web-gobbler
cd web-gobbler
docker compose up -d
```

Docker Compose starts six services:

| Service     | Purpose                          | Port |
|-------------|----------------------------------|------|
| `postgres`  | Job storage (PostgreSQL 16)      | 5432 |
| `redis`     | Job queue (Redis 7)              | 6379 |
| `api`       | REST API (Gin)                   | 8080 |
| `worker`    | Background scraper pool          | —    |
| `prometheus`| Metrics collection               | 9090 |
| `grafana`   | Metrics dashboard                | 3000 |

> [!NOTE]
> For AWS-shaped local practice (Terraform → Floci), use `./scripts/floci-up.sh` instead — see [How to run](../how-to/run.md#ways-to-run). Do not run Compose `api` and Floci ECS on port **8080** at the same time.

Wait for all services to become healthy:

```bash
docker compose ps
```

Every service should show `Up` and a green `(healthy)` status. This usually takes 5–10 seconds.

## Step 2 — Verify the API is running

```bash
curl http://localhost:8080/api/v1/health
```

You should see:

```json
{"status":"ok","db":"ok","redis":"ok","version":"1.0.0"}
```

> [!TIP]
> If you see `"status":"degraded"` wait a few seconds and try again. The API starts before PostgreSQL and Redis finish their initialisation.

## Step 3 — Submit a scraping job

Submit a job to scrape [example.com](https://example.com) for LLM-ready markdown (and links):

```bash
curl -X POST http://localhost:8080/api/v1/scrape \
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

Copy the `job_id` value — you will need it in the next step. The `poll_url` tells you where to check for results.

## Step 4 — Poll for results

The worker has already dequeued your job from Redis, scraped the page, and stored the result. Poll the job endpoint to see the outcome:

```bash
curl http://localhost:8080/api/v1/jobs/f47ac10b-58cc-4372-a567-0e02b2c3d479
```

Replace the ID with the one you received in Step 3.

When the job is still running you see:

```json
{"job_id":"f47ac10b-...","status":"queued","created_at":"..."}
```

When the job finishes the status changes to `"completed"` and the response includes the scraped content:

```json
{
  "job_id": "f47ac10b-...",
  "status": "completed",
  "url": "https://example.com",
  "created_at": "2025-09-01T10:32:00Z",
  "completed_at": "2025-09-01T10:32:03Z",
  "duration_ms": 1240,
  "results": {
    "links": ["https://www.iana.org/domains/example"],
    "headers": ["Example Domain"],
    "paragraphs": ["This domain is for use in illustrative examples in documents..."]
  },
  "meta": {
    "links_count": 1,
    "headers_count": 1,
    "paragraphs_count": 1,
    "http_status": 200,
    "retries_used": 0
  }
}
```

> [!TIP]
> Poll every 2–3 seconds. Most jobs complete within 1–2 seconds. Increase `timeout_seconds` for slow websites.

## Step 5 — View the Grafana dashboard

Open [http://localhost:3000](http://localhost:3000) in your browser. Grafana starts with a pre-provisioned Prometheus datasource and dashboard.

1. Log in with **admin** / **admin** (skip the password change prompt).
2. Navigate to **Dashboards** → **GoScrape Dashboard** (bundled dashboard title).
3. You will see five panels:
   - **Jobs per minute** — queued, completed, and failed jobs over time
   - **Job duration (P50/P95/P99)** — how long scrapes take
   - **Queue depth** — pending jobs in Redis
   - **HTTP error rate** — non-2xx responses grouped by type
   - **Retries total** — retry attempts across all jobs

Submit a few more jobs from Step 3 and watch the panels update in real time (Prometheus scrapes every 15 seconds).

## Step 6 — Run the tests

```bash
# Unit tests (scraper + API handlers)
go test -race -cover ./...

# Integration tests (spins up real PostgreSQL + Redis containers)
docker compose up -d postgres redis
go test -tags=integration -race -v ./test/integration/...
```

All 33 unit tests and 12 integration tests should pass.

## Step 7 — Tear down

```bash
docker compose down -v
```

The `-v` flag removes the named volumes so PostgreSQL and Redis data is cleaned up. Omit `-v` if you want to keep the data for your next session.

## What you have accomplished

- Started the full Web Gobbler stack locally
- Submitted an asynchronous scraping job
- Polled the API until the job completed
- Viewed scraped content (links, headers, paragraphs)
- Explored the Grafana dashboard
- Verified the test suite passes

You are now ready to use Web Gobbler for your own scraping tasks. Continue to the [How-to guides](../how-to/) for production deployment and advanced configuration.

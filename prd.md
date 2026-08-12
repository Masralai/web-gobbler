# PRD — Distributed Web Scraper API

**Project:** GoScrape API  
**Stack:** Go (Gin) · Redis · PostgreSQL · Docker · AWS ECS · GitHub Actions · Prometheus/Grafana  
**Status:** Proposed  
**Version:** 1.0

---

## 1. Overview

GoScrape API is a production-grade distributed web scraping service that wraps the existing CLI scraper into a horizontally scalable REST API. Clients submit scraping jobs via HTTP, jobs are queued and processed by a worker pool, and results are persisted to PostgreSQL. The system is fully containerised, deployed on AWS ECS, and ships with observability and a CI/CD pipeline.

This project directly extends the existing Go web scraper (`goquery`, `net/http`) and mirrors the CI/CD and backend automation work done at HireZapp.

---

## 2. Goals

| # | Goal |
|---|------|
| G1 | Expose scraping capability as a stateless REST API |
| G2 | Process jobs asynchronously via a worker pool — no blocking HTTP responses |
| G3 | Persist all results and job metadata to PostgreSQL |
| G4 | Scale horizontally: multiple ECS task instances, shared Redis queue |
| G5 | Ship production observability: metrics, structured logs, health checks |
| G6 | Zero-downtime deployments via GitHub Actions → ECS rolling update |

### Non-goals

- Browser/JS rendering (Chromium/Playwright) — plain HTTP only
- Authentication and multi-tenancy
- Frontend dashboard (Tier 3)
- Scheduled/cron-based scraping

---

## 3. Architecture

```
Client
  │
  ▼
Gin REST API  ──── POST /scrape ────►  Redis Job Queue
  │                                        │
  │◄─── GET /jobs/:id ────────────────     │
  │                                    Worker Pool (goroutines)
  │                                        │
  ▼                                        ▼
PostgreSQL  ◄──────────── persist results & job status
  │
  ▼
Prometheus ──► Grafana dashboard
```

All components run in Docker containers. In production, the API and worker containers are separate ECS services sharing the same Redis instance (ElastiCache) and RDS (PostgreSQL).

---

## 4. API Specification

### Base URL
`/api/v1`

---

### `POST /scrape`

Submit a new scraping job.

**Request body**
```json
{
  "url": "https://example.com",
  "extract": ["links", "headers", "paragraphs"],
  "options": {
    "timeout_seconds": 15,
    "max_retries": 3,
    "follow_redirects": true
  }
}
```

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `url` | string | yes | — | Must be valid `http://` or `https://` URL |
| `extract` | []string | no | `["links"]` | Any combination of `links`, `headers`, `paragraphs` |
| `options.timeout_seconds` | int | no | `10` | Per-request HTTP timeout |
| `options.max_retries` | int | no | `3` | Exponential backoff, max 3 attempts |
| `options.follow_redirects` | bool | no | `true` | |

**Response `202 Accepted`**
```json
{
  "job_id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "status": "queued",
  "created_at": "2025-09-01T10:32:00Z",
  "poll_url": "/api/v1/jobs/f47ac10b-58cc-4372-a567-0e02b2c3d479"
}
```

---

### `GET /jobs/:id`

Poll job status and retrieve results when complete.

**Response — in progress**
```json
{
  "job_id": "f47ac10b-...",
  "status": "processing",
  "created_at": "2025-09-01T10:32:00Z",
  "updated_at": "2025-09-01T10:32:01Z"
}
```

**Response — complete**
```json
{
  "job_id": "f47ac10b-...",
  "status": "completed",
  "url": "https://example.com",
  "created_at": "2025-09-01T10:32:00Z",
  "completed_at": "2025-09-01T10:32:03Z",
  "duration_ms": 1240,
  "results": {
    "links": ["https://example.com/about", "https://example.com/contact"],
    "headers": ["Welcome to Example", "Our Services"],
    "paragraphs": ["This domain is for use in illustrative examples..."]
  },
  "meta": {
    "links_count": 2,
    "headers_count": 2,
    "paragraphs_count": 1,
    "http_status": 200,
    "retries_used": 0
  }
}
```

**Response — failed**
```json
{
  "job_id": "f47ac10b-...",
  "status": "failed",
  "error": "target returned 403 after 3 retries",
  "created_at": "2025-09-01T10:32:00Z",
  "failed_at": "2025-09-01T10:32:08Z"
}
```

---

### `GET /jobs`

List recent jobs (paginated).

**Query params:** `page`, `limit` (default 20), `status` (filter by `queued|processing|completed|failed`)

**Response `200 OK`**
```json
{
  "jobs": [ /* array of job summary objects */ ],
  "total": 142,
  "page": 1,
  "limit": 20
}
```

---

### `DELETE /jobs/:id`

Cancel a queued job (no-op if already processing or done).

**Response `200 OK`** or **`409 Conflict`** if the job cannot be cancelled.

---

### `GET /health`

Liveness and readiness check for ECS.

**Response `200 OK`**
```json
{
  "status": "ok",
  "db": "ok",
  "redis": "ok",
  "version": "1.0.0"
}
```

---

## 5. Data Model

### `jobs` table (PostgreSQL)

```sql
CREATE TABLE jobs (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  url           TEXT NOT NULL,
  extract       TEXT[] NOT NULL,
  options       JSONB,
  status        TEXT NOT NULL DEFAULT 'queued',  -- queued|processing|completed|failed
  result        JSONB,
  error_msg     TEXT,
  http_status   INT,
  retries_used  INT DEFAULT 0,
  duration_ms   INT,
  created_at    TIMESTAMPTZ DEFAULT now(),
  updated_at    TIMESTAMPTZ DEFAULT now(),
  completed_at  TIMESTAMPTZ
);

CREATE INDEX idx_jobs_status ON jobs(status);
CREATE INDEX idx_jobs_created_at ON jobs(created_at DESC);
```

### Redis queue schema

- Key: `scraper:jobs:queue` — Redis List (LPUSH to enqueue, BRPOP to dequeue)
- Value: JSON-encoded job payload `{ "job_id": "...", "url": "...", ... }`
- TTL: none on the queue key itself; processed job IDs are deleted after persisting to PostgreSQL

---

## 6. Worker Pool

The worker subsystem is a separate Go binary (or the same binary with a `--mode=worker` flag) that:

1. Calls `BRPOP scraper:jobs:queue` (blocking pop, 5s timeout) in a loop
2. Deserialises the job payload
3. Updates job status to `processing` in PostgreSQL
4. Calls the core scraper logic (reusing existing `goquery` extraction functions)
5. Applies retry logic: up to `max_retries` attempts with exponential backoff (`100ms * 2^n`)
6. Persists results/error to PostgreSQL and sets final status

**Concurrency:** Each worker container runs `N` goroutines in parallel, where `N` is configurable via `WORKER_CONCURRENCY` env var (default `5`). Goroutines share a `sync.WaitGroup` and are rate-limited per-domain using `golang.org/x/time/rate` (default `2 req/s` per domain).

---

## 7. Rate Limiting

| Scope | Mechanism | Default |
|---|---|---|
| Per-domain request rate | `golang.org/x/time/rate` token bucket, keyed by hostname | 2 req/s |
| API inbound | Gin middleware, per-IP | 60 req/min |
| Job queue depth | Configurable max queue length before `503` | 1,000 jobs |

---

## 8. Error Handling

All errors from the scraper core use typed sentinel errors:

```
ErrInvalidURL       — bad URL format, rejected at API layer
ErrHTTPFailed       — non-2xx after all retries
ErrParseFailure     — goquery could not parse the response body
ErrTimeout          — request exceeded configured timeout
ErrQueueFull        — Redis queue at capacity
```

HTTP status codes returned by the API:

| Scenario | HTTP Code |
|---|---|
| Valid job submitted | `202 Accepted` |
| Malformed request body | `400 Bad Request` |
| Job not found | `404 Not Found` |
| Queue full | `503 Service Unavailable` |
| Internal error | `500 Internal Server Error` |

---

## 9. Observability

### Metrics (Prometheus via `prometheus/client_golang`)

| Metric | Type | Labels |
|---|---|---|
| `scraper_jobs_total` | Counter | `status` (queued/completed/failed) |
| `scraper_job_duration_ms` | Histogram | `extract_type` |
| `scraper_queue_depth` | Gauge | — |
| `scraper_http_errors_total` | Counter | `error_type` |
| `scraper_retries_total` | Counter | — |

Exposed at `GET /metrics` (Prometheus scrape endpoint).

### Logging

Structured JSON logs via `log/slog` (Go 1.21+):

```json
{
  "time": "2025-09-01T10:32:03Z",
  "level": "INFO",
  "msg": "job completed",
  "job_id": "f47ac10b-...",
  "url": "https://example.com",
  "duration_ms": 1240,
  "links_count": 14
}
```

Log levels: `DEBUG` in development, `INFO` in production. No sensitive URLs in `ERROR`-level logs.

### Grafana dashboard panels

- Jobs per minute (queued / completed / failed)
- P50/P95/P99 job duration
- Queue depth over time
- HTTP error rate by type
- Worker concurrency utilisation

---

## 10. Infrastructure

### Docker

Two images:

```
goscrape-api      — Gin HTTP server
goscrape-worker   — Worker pool binary
```

Both use a multi-stage build: `golang:1.23-alpine` builder → `alpine:3.20` runtime. Final image under 20 MB.

`docker-compose.yml` for local development: `api`, `worker`, `postgres`, `redis`, `grafana`, `prometheus` services.

### AWS (production)

| Component | AWS Service |
|---|---|
| Container orchestration | ECS Fargate |
| Container registry | ECR |
| PostgreSQL | RDS (db.t4g.micro) |
| Redis | ElastiCache (cache.t4g.micro) |
| Secrets | AWS Secrets Manager |
| Logs | CloudWatch Logs |

ECS task definitions for `api` (min 1, max 3 tasks) and `worker` (min 1, max 5 tasks). Workers scale on `scraper_queue_depth` via a CloudWatch alarm → Application Auto Scaling.

### Environment variables

```
DATABASE_URL          postgresql://user:pass@host:5432/goscrape
REDIS_URL             redis://host:6379
PORT                  8080
WORKER_CONCURRENCY    5
DEFAULT_TIMEOUT_SEC   10
DEFAULT_MAX_RETRIES   3
LOG_LEVEL             INFO
```

---

## 11. CI/CD (GitHub Actions)

### Pipeline: `.github/workflows/deploy.yml`

Triggered on push to `main`.

```
Lint & vet
  │
  ▼
Unit tests (go test ./...)
  │
  ▼
Integration tests (Docker Compose with real Redis + Postgres)
  │
  ▼
Build Docker images (api + worker)
  │
  ▼
Push to ECR
  │
  ▼
Update ECS task definitions
  │
  ▼
ECS rolling deploy (api service, then worker service)
  │
  ▼
Health check: poll GET /health until 200 or timeout
```

On failure at any step, the pipeline stops and sends a notification. No manual approval gate for now; can be added for production promotion.

---

## 12. Project Structure

```
goscrape/
├── cmd/
│   ├── api/          main.go — Gin server entrypoint
│   └── worker/       main.go — Worker pool entrypoint
├── internal/
│   ├── api/          handlers, middleware, routes
│   ├── scraper/      core extraction logic (ported from CLI)
│   ├── queue/        Redis enqueue/dequeue helpers
│   ├── store/        PostgreSQL CRUD (pgx)
│   └── metrics/      Prometheus registration
├── migrations/       SQL migration files (golang-migrate)
├── docker/
│   ├── Dockerfile.api
│   └── Dockerfile.worker
├── docker-compose.yml
├── .github/workflows/deploy.yml
└── README.md
```

---

## 13. Milestones

| Day | Deliverable |
|---|---|
| 1 | Repo scaffolded, Docker Compose running locally (Gin + Redis + Postgres) |
| 2 | `POST /scrape` + `GET /jobs/:id` working end-to-end locally |
| 3 | Worker pool with concurrency, rate limiting, retry logic |
| 4 | Prometheus metrics + Grafana dashboard wired up locally |
| 5 | ECS task definitions written, ECR push working |
| 6 | GitHub Actions pipeline: lint → test → build → deploy |
| 7 | Integration tests, `GET /jobs` pagination, `DELETE /jobs/:id`, README |

---

## 14. Testing Strategy

| Layer | Tool | Coverage target |
|---|---|---|
| Unit — scraper core | `go test` | >80% of extraction logic |
| Unit — handlers | `net/http/httptest` + mock store | all HTTP status code paths |
| Integration | Docker Compose + `testcontainers-go` | full POST → queue → worker → DB → GET flow |
| Load | `k6` | 50 concurrent job submissions, p95 < 2s queue time |

---

## 15. Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| Target sites returning 403/captcha | High | Configurable User-Agent, per-domain rate limiting, graceful `failed` status |
| Redis losing unprocessed jobs on restart | Low | Use Redis persistence (AOF), consider DLQ for failed pops |
| ECS task OOM on large pages | Low | Set `max_body_size` limit (default 5 MB) in HTTP client |
| PostgreSQL connection exhaustion | Medium | Use `pgxpool` with max 10 connections per worker container |

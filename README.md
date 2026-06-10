# GoScrape API

Distributed web scraping service built with Go (Gin), Redis, PostgreSQL, and Docker.

Submit scraping jobs via HTTP, process them asynchronously through a worker pool, and retrieve results via a REST API. Horizontally scalable on AWS ECS Fargate.

## Architecture

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

## Features

- Async job processing — non-blocking HTTP responses, workers pick jobs from Redis
- Configurable extraction — links, headers, paragraphs from any URL
- Per-domain rate limiting — 2 req/s default per hostname
- Retry with exponential backoff — 100ms × 2ⁿ, configurable max attempts
- Metrics — Prometheus counters/histograms/gauges, Grafana dashboard
- Pagination — list jobs with status filtering and pagination
- Health checks — liveness/readiness with DB + Redis status
- Production-ready — distroless Docker images, Terraform for AWS, CI/CD

## Local development

### Prerequisites

- Go 1.26+
- Docker and Docker Compose

### Start services

```bash
docker compose up -d
```

This starts: API (port 8080), worker, PostgreSQL (5432), Redis (6379), Prometheus (9090), Grafana (3000).

### Verify

```bash
curl http://localhost:8080/api/v1/health
```

```json
{"status":"ok","db":"ok","redis":"ok","version":"1.0.0"}
```

### Run migrations manually

Migrations run automatically via init scripts; manual:

```bash
docker compose exec postgres psql -U user -d goscrape -f /migrations/000001_create_jobs_table.up.sql
```

## API documentation

Base URL: `/api/v1`

### POST /scrape

Submit a scraping job.

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

Response `202 Accepted`:
```json
{
  "job_id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "status": "queued",
  "created_at": "2025-09-01T10:32:00Z",
  "poll_url": "/api/v1/jobs/f47ac10b-58cc-4372-a567-0e02b2c3d479"
}
```

### GET /jobs/:id

Poll job status and retrieve results.

Response `200 OK` — completed:
```json
{
  "job_id": "f47ac10b-...",
  "status": "completed",
  "url": "https://example.com",
  "created_at": "2025-09-01T10:32:00Z",
  "completed_at": "2025-09-01T10:32:03Z",
  "duration_ms": 1240,
  "results": {
    "links": ["https://example.com/about"],
    "headers": ["Welcome to Example"],
    "paragraphs": ["This domain is for use in illustrative examples..."]
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

Response `200 OK` — failed:
```json
{
  "job_id": "f47ac10b-...",
  "status": "failed",
  "error": "target returned 403 after 3 retries",
  "created_at": "2025-09-01T10:32:00Z",
  "failed_at": "2025-09-01T10:32:08Z"
}
```

Response `404` — job not found.

### GET /jobs

List jobs with pagination and optional status filter.

Query params: `page` (default 1), `limit` (default 20, max 100), `status` (queued|processing|completed|failed)

Response `200 OK`:
```json
{
  "jobs": [
    {
      "job_id": "f47ac10b-...",
      "url": "https://example.com",
      "status": "completed",
      "created_at": "2025-09-01T10:32:00Z",
      "updated_at": "2025-09-01T10:32:03Z"
    }
  ],
  "total": 1,
  "page": 1,
  "limit": 20
}
```

### DELETE /jobs/:id

Cancel a queued job. No-op if already processing or done.

Response `200 OK`: `{"status": "cancelled"}`
Response `409 Conflict`: job cannot be cancelled
Response `404`: job not found

### GET /health

Liveness and readiness check.

Response `200 OK`:
```json
{"status":"ok","db":"ok","redis":"ok","version":"1.0.0"}
```

Response `503` if DB or Redis is unreachable:
```json
{"status":"degraded","db":"error: connection refused","redis":"ok","version":"1.0.0"}
```

## Running tests

```bash
# Unit tests (34 tests across scraper + API handlers)
go test -race -cover ./...

# Integration tests (requires Docker for PostgreSQL + Redis containers)
docker compose up -d postgres redis
go test -tags=integration -race -v ./test/integration/...
docker compose down

# Load tests (requires k6: brew install k6)
k6 run test/load/scenario.js
```

## Production deployment

### Prerequisites

- AWS account with permissions for ECS, ECR, RDS, ElastiCache, IAM
- Terraform installed
- Docker installed

### Infrastructure

```bash
# Initialize Terraform
cd terraform
terraform init

# Review and apply
terraform plan
terraform apply
```

This provisions VPC, RDS PostgreSQL, ElastiCache Redis, ECR repositories, ECS Fargate cluster with API and worker services, ALB, and auto-scaling.

### CI/CD

Push to `main` triggers the GitHub Actions pipeline:

1. Lint and vet
2. Unit tests
3. Integration tests
4. Build Docker images (api + worker)
5. Push to ECR
6. Register new ECS task definitions
7. Rolling deploy (api → worker)
8. Health check

### Required GitHub secrets

| Secret | Description |
|--------|-------------|
| `AWS_ACCESS_KEY_ID` | IAM user access key |
| `AWS_SECRET_ACCESS_KEY` | IAM user secret key |
| `ALB_DNS` | ALB DNS name for health checks |

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | API server port |
| `DATABASE_URL` | `postgresql://user:pass@localhost:5432/goscrape` | PostgreSQL connection string |
| `REDIS_URL` | `redis://localhost:6379` | Redis connection string |
| `WORKER_CONCURRENCY` | `5` | Goroutines per worker |
| `DEFAULT_TIMEOUT_SEC` | `10` | HTTP timeout for scrapers |
| `DEFAULT_MAX_RETRIES` | `3` | Max retry attempts per job |
| `SCRAPER_RATE_LIMIT` | `2` | Per-domain requests per second |
| `LOG_LEVEL` | `INFO` | Log level (DEBUG, INFO, WARN, ERROR) |
| `GOSCRAPE_ENVIRONMENT` | `prod` | Environment label for CloudWatch metrics |

## Project structure

```
├── cmd/
│   ├── api/             main.go — Gin server entrypoint
│   └── worker/          main.go — Worker pool entrypoint
├── internal/
│   ├── api/             Handlers, middleware, types, validation
│   ├── scraper/         Core extraction logic (goquery)
│   ├── queue/           Redis enqueue/dequeue helpers
│   ├── store/           PostgreSQL CRUD (pgx)
│   └── metrics/         Prometheus metric registration
├── migrations/          SQL migration files
├── docker/              Dockerfiles, Prometheus config, Grafana dashboard
├── terraform/           AWS infrastructure as code
├── test/
│   ├── integration/     Testcontainers integration tests
│   └── load/            k6 load test scripts
├── .github/workflows/   CI/CD pipeline
├── docker-compose.yml   Local development environment
└── README.md
```

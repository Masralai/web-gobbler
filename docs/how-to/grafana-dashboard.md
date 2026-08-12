# How to use the Grafana dashboard

The Grafana dashboard gives you real-time visibility into scraping performance, queue health, and error rates. This guide explains each panel and how to interpret the data.

## Accessing Grafana

In local development, Grafana runs at [http://localhost:3000](http://localhost:3000):

- Default credentials: **admin** / **admin**
- The Prometheus datasource and GoScrape dashboard are auto-provisioned

In production, Grafana must be configured separately (it is not included in the Terraform deployment). You can use Grafana Cloud or self-host Grafana pointing to the Prometheus endpoint at `/metrics` on the ALB.

## Dashboard layout

The GoScrape dashboard has five panels, all updated every 15 seconds (matching the Prometheus scrape interval).

### Panel 1 — Jobs per minute

**Metric:** `rate(scraper_jobs_total[1m])`

This panel shows how many jobs are being queued, completed, and failed per minute.

- **Queued (blue):** spikes indicate high submission rates. Sustainably high queue growth means you may need to scale up workers.
- **Completed (green):** should closely follow the queued rate in steady state. A widening gap between queued and completed suggests the worker pool is saturated.
- **Failed (red):** persistent failures indicate configuration problems (timeout too low, target site blocking scraper) or SSRF blocks.

### Panel 2 — Job duration (P50 / P95 / P99)

**Metric:** `histogram_quantile(0.50/0.95/0.99, rate(scraper_job_duration_ms_bucket[5m]))`

Shows how long scrapes take to complete, broken down by percentile.

- **P50 (median):** typical scrape duration. Most pages should complete in 1–3 seconds.
- **P95:** the slowest 5% of scrapes. If this exceeds your `timeout_seconds` setting, some pages may be timing out.
- **P99:** outliers. Investigate these — they are often caused by extremely slow or unresponsive targets.

A sudden P95/P99 spike may indicate the target site is rate-limiting the scraper or the network path has degraded.

### Panel 3 — Queue depth

**Metric:** `scraper_queue_depth`

The current number of jobs waiting in the Redis queue.

- **Depth > 0:** jobs are backlogged. The workers are processing at their maximum rate.
- **Depth growing:** submissions outpace processing. The system needs more workers.
- **Depth sustained at 0:** the worker pool has spare capacity.

In production, this metric drives the worker auto-scaling policy. A CloudWatch alarm triggers a scale-out when depth exceeds 10 for two consecutive 60-second periods.

### Panel 4 — HTTP error rate

**Metric:** `rate(scraper_http_errors_total[1m])`

Shows the rate of non-2xx HTTP responses grouped by error type.

- **http_failed:** the target server returned a 4xx or 5xx status. 403/404 errors are common; 5xx errors may indicate server load issues.
- Other error types appear here if the codebase is extended to classify failures more granularly (e.g. `timeout`, `dns_failure`, `ssrf_blocked`).

Correlate spikes in this panel with the Failed counter in Panel 1.

### Panel 5 — Retries total

**Metric:** `scraper_retries_total`

A cumulative counter of every retry attempt across all jobs.

- **Steady growth:** expected — the scraper retries transient failures with exponential backoff.
- **Accelerating slope:** too many jobs are failing on the first attempt. Check Panel 4 for the error type and consider increasing `DEFAULT_TIMEOUT_SEC` or `DEFAULT_MAX_RETRIES`.

## Creating custom panels

You can extend the dashboard with additional panels using any of the exposed metrics:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `scraper_jobs_total` | Counter | `status` | Jobs by status |
| `scraper_job_duration_ms` | Histogram | `extract_type` | Duration distribution |
| `scraper_retries_total` | Counter | — | Total retries |
| `scraper_queue_depth` | Gauge | — | Current queue depth |
| `scraper_http_errors_total` | Counter | `error_type` | HTTP errors by type |

Example PromQL to show jobs by extract type:

```promql
rate(scraper_job_duration_ms_count[1m])
```

Example PromQL to calculate the error rate as a percentage of total jobs:

```promql
(
  rate(scraper_http_errors_total[5m])
  /
  rate(scraper_jobs_total[5m])
) * 100
```

## Troubleshooting

### No data in the dashboard

1. Verify Prometheus is running: `curl http://localhost:9090/-/healthy`
2. Check that Prometheus can reach the API metrics endpoint: `curl http://localhost:9090/api/v1/targets` — the `api:8080` target should be `UP`
3. Verify the API `/metrics` endpoint returns data: `curl http://localhost:8080/metrics`

### The dashboard is not auto-provisioned

In local development, provisioning is handled by Docker Compose volumes. If the dashboard is missing after a restart:

```bash
docker compose down -v
docker compose up -d
```

The `-v` flag removes old Grafana volumes so provisioning runs fresh.

If you are running Grafana manually, import `docker/grafana/dashboard.json` and configure the Prometheus datasource URL to `http://prometheus:9090` (Docker internal DNS) or `http://localhost:9090` (host networking).

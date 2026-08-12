# Job lifecycle and retry semantics

This document explains the complete journey of a scrape job from submission to completion, including the retry mechanism and status state machine.

## Status state machine

Every job transitions through a sequence of statuses:

```
                    ┌──────────┐
                    │  queued  │
                    └────┬─────┘
                         │
                         ▼
                    ┌──────────┐
          ┌─────────│processing│─────────┐
          │         └──────────┘         │
          │           │                  │
          ▼           ▼                  ▼
    ┌──────────┐ ┌──────────┐     ┌──────────┐
    │completed │ │  failed  │     │  failed  │
    └──────────┘ └──────────┘     └──────────┘
                                  (cancelled)
```

The three terminal statuses are:

- **`completed`** — the page was successfully scraped and the result is stored.
- **`failed`** — all retry attempts were exhausted, or the job was manually cancelled.
- **`failed` (cancelled)** — a user called `DELETE /jobs/:id` while the job was still `queued`. The `error_msg` is set to `"cancelled"` to distinguish this from a scrape failure.

A job can only be cancelled when its status is `queued`. If a client tries to cancel a `processing`, `completed`, or `failed` job, the API returns `409 Conflict`.

## Journey of a job

### 1. Submission (POST /api/v1/scrape)

```
Client ──POST──► API ──INSERT──► PostgreSQL (status=queued)
                    ──LPUSH──► Redis
                    ──202──► Client
```

The API handler:

1. Validates the request body (URL format, extract types, option bounds).
2. Inserts a row in PostgreSQL with status `queued`.
3. Pushes a JSON payload to the Redis list `scraper:jobs:queue` containing the job ID, URL, extract types, and optional overrides.
4. Returns `202 Accepted` with the `job_id` and `poll_url`.

The response is sent before the worker picks up the job. This decouples submission from processing.

### 2. Dequeue (Worker loop)

```
Worker ──BRPOP──► Redis (5s timeout)
         ──job──► Worker
```

Each worker goroutine calls `BRPOP` on the Redis list with a 5-second timeout. If a job is available, Redis returns it immediately. If the queue is empty, the goroutine blocks for up to 5 seconds before looping.

Unexpectected disconnects are handled by go-redis's automatic reconnection. If the connection drops mid-BRPOP, the command is retried transparently.

### 3. Claim (status → processing)

```
Worker ──UPDATE──► PostgreSQL (status=processing)
```

The worker marks the job as `processing` in PostgreSQL. This serves two purposes:

- **Visibility** — the client polling `GET /jobs/:id` sees the job is being worked on.
- **Idempotency** — if the worker crashes after this point, the job stays as `processing`. A separate reaper process (not yet implemented) can detect stuck jobs and re-enqueue them.

The claim is a single SQL `UPDATE` with no locking — it is safe because only one worker receives each job from Redis at a time.

### 4. Rate limit wait

```
Worker ──Wait──► perDomainRateLimiter
```

Before scraping, the worker waits for its per-domain token bucket. The default rate is 2 requests per second per hostname. If the same domain appears in multiple queued jobs, workers serialise their requests to respect the rate limit.

The rate limiter uses `golang.org/x/time/rate.Limiter.Wait(ctx)`, which respects context cancellation — if the worker pool is shutting down, the wait is interrupted.

### 5. Scrape (with retries)

```
Worker ──ScrapePage──► Target URL
         success ──► goto step 6
         failure ──► backoff 100ms×2ⁿ ──► retry ──► ScrapePage
```

The scrape uses a custom `http.Client` configured with:

- **Timeout** — configurable per-job via `options.timeout_seconds` (default 10 s).
- **Redirect policy** — follows up to 5 redirects, each SSRF-checked. If `follow_redirects` is false, the first redirect response is returned as-is.
- **User-Agent** — `GoScrape/1.0` (overridable in `DefaultOptions`).
- **Body limit** — 5 MB max response body via `io.LimitReader`.

**Retry loop:**

```
for attempt := 0; attempt < maxRetries; attempt++ {
    if attempt > 0 {
        backoff = 100ms × 2^(attempt-1)   // 100ms, 200ms, 400ms, ...
        sleep(backoff)                      // also checks ctx.Done()
        metrics.RetriesTotal++
    }
    result, err = ScrapePage(ctx, url, extract, opts)
    if err == nil {
        break
    }
}
```

The rationale for exponential backoff: transient failures (connection resets, DNS hiccups, rate limiting) usually resolve within a few hundred milliseconds. A fixed backoff would either hammer the target or waste time. Exponential gives the fastest recovery while remaining polite.

**What is retried?** Any error from `ScrapePage` triggers a retry: HTTP non-2xx (`ErrHTTPFailed`), timeout, DNS failure, parse failure. SSRF blocks (`ErrPrivateIP`) and invalid URLs (`ErrInvalidURL`) are not retried because they will always fail.

### 6. Persist

```
Worker ──UPDATE──► PostgreSQL
         success ──► metrics.JobsTotal{completed}++
         failure ──► metrics.JobsTotal{failed}++ , metrics.HTTPErrorsTotal++ (if applicable)
```

On success: the worker stores the `Result` (links, headers, paragraphs, HTTP status, duration) and sets status to `completed`.

On failure: the worker stores any partial result (HTTP status if the response was received) and the error message, then sets status to `failed`.

### 7. Poll (GET /api/v1/jobs/:id)

```
Client ──GET──► API ──SELECT──► PostgreSQL ──result──► API ──200──► Client
```

The client polls the `poll_url` returned in the submission response. The handler returns:

- **`queued` / `processing`** — the job is not yet done. Keep polling.
- **`completed`** — full result with links, headers, paragraphs, and metadata.
- **`failed`** — error message describing what went wrong.

There is no webhook or callback mechanism. Polling is the intended pattern.

## Timing considerations

| Phase | Typical duration | Notes |
|-------|-----------------|-------|
| Enqueue + claim | < 10 ms | Redis LPUSH + PostgreSQL UPDATE |
| Rate limit wait | 0–500 ms | Depends on domain concurrency |
| Scrape | 500 ms – 5 s | Slow pages with many resources take longer |
| Persist | < 10 ms | Single-row UPDATE |
| **Total** | **~1–5 s** | Most pages complete within 3 seconds |

If a job exceeds the total timeout (`options.timeout_seconds` × `maxRetries`), it will fail with a timeout error and `retries_used` reflecting how many complete attempts were made.

## Partial results

When a scrape fails after exhausting retries, the partial result (if any) includes the HTTP status code and duration of the last attempt. This is useful for debugging:

```json
{
  "status": "failed",
  "error": "HTTP request failed with non-2xx status: 403",
  "meta": {
    "http_status": 403,
    "retries_used": 3
  }
}
```

Partial results are never returned for successful scrapes — the full `results` object is always populated on success.

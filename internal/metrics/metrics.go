// Package metrics registers and exposes Prometheus instrumentation for the GoScrape service.
// All metrics are auto-registered via promauto and are served at the /metrics endpoint.
// Five metrics are exposed: scraper_jobs_total (counter, status label),
// scraper_job_duration_ms (histogram, extract_type label), scraper_retries_total (counter),
// scraper_queue_depth (gauge), and scraper_http_errors_total (counter, error_type label).
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// JobsTotal counts completed, failed, and queued jobs (label: status).
	JobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "scraper_jobs_total",
		Help: "Total number of jobs processed by status",
	}, []string{"status"})

	// JobDuration measures scrape duration in milliseconds (label: extract_type).
	JobDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "scraper_job_duration_ms",
		Help:    "Job duration in milliseconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"extract_type"})

	// RetriesTotal counts every retry attempt across all jobs.
	RetriesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "scraper_retries_total",
		Help: "Total number of retry attempts",
	})

	// QueueDepth reports the current number of jobs waiting in the Redis queue.
	QueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "scraper_queue_depth",
		Help: "Current queue depth",
	})

	// HTTPErrorsTotal counts non-2xx HTTP responses grouped by error type (label: error_type).
	HTTPErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "scraper_http_errors_total",
		Help: "Total HTTP errors by type",
	}, []string{"error_type"})
)

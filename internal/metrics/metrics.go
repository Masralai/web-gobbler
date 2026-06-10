package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	JobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "scraper_jobs_total",
		Help: "Total number of jobs processed by status",
	}, []string{"status"})

	JobDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "scraper_job_duration_ms",
		Help:    "Job duration in milliseconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"extract_type"})

	RetriesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "scraper_retries_total",
		Help: "Total number of retry attempts",
	})

	QueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "scraper_queue_depth",
		Help: "Current queue depth",
	})

	HTTPErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "scraper_http_errors_total",
		Help: "Total HTTP errors by type",
	}, []string{"error_type"})
)

func Register(r *prometheus.Registry) {
	r.MustRegister(JobsTotal, JobDuration, RetriesTotal, QueueDepth, HTTPErrorsTotal)
}

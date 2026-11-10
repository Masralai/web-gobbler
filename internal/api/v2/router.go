package v2

import (
	"time"

	"github.com/Masralai/web-gobbler/internal/extract"
	"github.com/Masralai/web-gobbler/internal/queue"
	"github.com/Masralai/web-gobbler/internal/store"
	"github.com/gin-gonic/gin"
)

type Config struct {
	APIKey            string
	WebhookSecret     string
	SearXNGEndpoint   string
	PublicBaseURL     string
	DemoMode          bool
	MaxCrawlPages     int
	MaxConcurrentJobs int
	RetentionMinutes  int
	DefaultTimeout    time.Duration
}

type Handler struct {
	Store      *store.Store
	Queue      *queue.Queue
	Extractor  extract.Extractor
	LLMEnabled bool
	Config     Config
}

func NewHandler(s *store.Store, q *queue.Queue, cfg Config) *Handler {
	return &Handler{Store: s, Queue: q, Config: cfg}
}

func (h *Handler) Register(r *gin.Engine) {
	r.GET("/", h.HandleDemo)
	r.GET("/demo", h.HandleDemo)
	g := r.Group("/v2")
	g.Use(h.authMiddleware())

	g.POST("/scrape", h.HandleScrape)
	g.GET("/scrape/:jobId", h.HandleScrapeStatus)
	g.POST("/scrape/:jobId/interact", h.HandleInteract)
	g.DELETE("/scrape/:jobId/interact", h.HandleInteract)

	g.POST("/crawl", h.HandleCrawl)
	g.GET("/crawl/ongoing", h.HandleOngoingCrawls)
	g.GET("/crawl/active", h.HandleOngoingCrawls)
	g.POST("/crawl/params-preview", h.HandleCrawlPreview)
	g.GET("/crawl/:jobId", h.HandleCrawlStatus)
	g.DELETE("/crawl/:jobId", h.HandleCrawlCancel)
	g.GET("/crawl/:jobId/errors", h.HandleCrawlErrors)

	g.POST("/batch/scrape", h.HandleBatch)
	g.GET("/batch/scrape/:jobId", h.HandleCrawlStatus)
	g.DELETE("/batch/scrape/:jobId", h.HandleCrawlCancel)
	g.GET("/batch/scrape/:jobId/errors", h.HandleCrawlErrors)

	g.POST("/map", h.HandleMap)
	g.POST("/search", h.HandleSearch)
	g.GET("/search", h.HandleSearch)
	g.POST("/extract", h.HandleExtract)
	g.GET("/extract/:jobId", h.HandleExtractStatus)
	g.GET("/parse/formats", h.HandleParseFormats)
	g.POST("/parse", h.HandleParse)
	g.GET("/concurrency-check", h.HandleConcurrency)
}

func (h *Handler) HandleInteract(c *gin.Context) {
	writeError(c, 501, CodeNotImplemented, "interactive browser sessions are not available in this deployment")
}

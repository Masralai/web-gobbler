package v2

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Masralai/web-gobbler/internal/crawl"
	"github.com/Masralai/web-gobbler/internal/queue"
	"github.com/Masralai/web-gobbler/internal/scraper"
	"github.com/Masralai/web-gobbler/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) HandleCrawl(c *gin.Context) {
	var req CrawlRequest
	if err := decodeJSON(c, &req, maxJSONBody); err != nil {
		writeDecodeError(c, err)
		return
	}
	if err := validateURL(req.URL); err != nil {
		writeBadRequest(c, err.Error())
		return
	}
	limit := 10000
	if req.Limit != nil {
		limit = *req.Limit
	}
	if limit < 1 || limit > 100000 {
		writeBadRequest(c, "limit must be between 1 and 100000")
		return
	}
	if h.Config.DemoMode && limit > h.demoMaxCrawl() {
		writeBadRequest(c, fmt.Sprintf("demo crawl limit is %d", h.demoMaxCrawl()))
		return
	}
	if req.Sitemap != "" && req.Sitemap != "skip" && req.Sitemap != "include" && req.Sitemap != "only" {
		writeBadRequest(c, "sitemap must be skip, include, or only")
		return
	}
	if err := validateWebhook(req.Webhook); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	if key := c.GetHeader("x-idempotency-key"); key != "" {
		if len(key) > 256 {
			writeBadRequest(c, "x-idempotency-key must be at most 256 characters")
			return
		}
		if _, found, err := h.Store.FindIdempotency(c.Request.Context(), key); err != nil {
			writeError(c, 500, CodeUnknown, "failed to read idempotency key")
			return
		} else if found {
			writeError(c, http.StatusConflict, CodeIdempotencyReused, "Idempotency key already used")
			return
		}
	}

	job := &store.V2Job{Kind: store.KindCrawl, URL: req.URL, Request: mustJSON(req), Webhook: mustJSON(req.Webhook)}
	id, err := h.Store.CreateV2Job(c.Request.Context(), job)
	if err != nil {
		writeError(c, 500, CodeUnknown, "failed to create crawl")
		return
	}
	if key := c.GetHeader("x-idempotency-key"); key != "" {
		_, _ = h.Store.SaveIdempotency(c.Request.Context(), key, id)
	}
	if err := h.Queue.PublishStream(c.Request.Context(), queue.StreamDefault, queue.StreamPayload{
		JobID: id.String(), Kind: store.KindCrawl, URL: req.URL,
	}); err != nil {
		writeError(c, 500, CodeUnknown, "failed to queue crawl")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "id": id.String(), "url": h.baseURL(c) + "/v2/crawl/" + id.String()})
}

func (h *Handler) HandleCrawlStatus(c *gin.Context) {
	id, ok := parseJobID(c)
	if !ok {
		return
	}
	job, err := h.Store.GetV2Job(c.Request.Context(), id)
	if err != nil {
		writeNotFound(c, "Job not found")
		return
	}
	completed, _, _, total, err := h.Store.V2ChildCounts(c.Request.Context(), id)
	if err != nil {
		writeError(c, 500, CodeUnknown, "failed to read crawl")
		return
	}
	skip, _ := atoiDefault(c.Query("skip"), 0)
	limit, _ := atoiDefault(c.Query("limit"), 100)
	if skip < 0 {
		skip = 0
	}
	if limit < 1 || limit > 100 {
		limit = 100
	}
	children, err := h.Store.ListV2Children(c.Request.Context(), id, "completed", limit, skip)
	if err != nil {
		writeError(c, 500, CodeUnknown, "failed to read crawl pages")
		return
	}
	data := make([]Document, 0, len(children))
	for _, ch := range children {
		if len(ch.Result) == 0 {
			continue
		}
		var doc Document
		if json.Unmarshal(ch.Result, &doc) == nil {
			data = append(data, doc)
		}
	}
	status := wireStatus(job.Status)
	if job.Status == "failed" {
		c.JSON(http.StatusOK, gin.H{
			"success": false, "status": "failed", "error": stringOr(job.ErrorMsg, "crawl failed"),
			"completed": completed, "total": total, "creditsUsed": 0,
			"expiresAt": h.expiresAt(job.CreatedAt), "data": []Document{},
		})
		return
	}
	next := ""
	if skip+len(data) < completed {
		next = fmt.Sprintf("%s/v2/%s?skip=%d&limit=%d", h.baseURL(c), crawlPath(c), skip+len(data), limit)
	}
	c.JSON(http.StatusOK, CrawlStatusResponse{
		Success: true, Status: status, Completed: completed, Total: total,
		CreditsUsed: 0, ExpiresAt: h.expiresAt(job.CreatedAt), Next: next, Data: data,
	})
}

func (h *Handler) HandleCrawlCancel(c *gin.Context) {
	id, ok := parseJobID(c)
	if !ok {
		return
	}
	status, err := h.Store.CancelV2Job(c.Request.Context(), id)
	if err != nil {
		writeNotFound(c, "Job not found")
		return
	}
	if status == "completed" {
		c.JSON(http.StatusConflict, gin.H{"error": "Crawl is already completed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}

func (h *Handler) HandleCrawlErrors(c *gin.Context) {
	id, ok := parseJobID(c)
	if !ok {
		return
	}
	if _, err := h.Store.GetV2Job(c.Request.Context(), id); err != nil {
		writeNotFound(c, "Job not found")
		return
	}
	children, err := h.Store.ListV2Children(c.Request.Context(), id, "failed", 100, 0)
	if err != nil {
		writeError(c, 500, CodeUnknown, "failed to read crawl errors")
		return
	}
	out := make([]CrawlError, 0, len(children))
	for _, ch := range children {
		out = append(out, CrawlError{
			ID: ch.ID.String(), Timestamp: ch.UpdatedAt.Format(time.RFC3339),
			URL: ch.URL, Error: stringOr(ch.ErrorMsg, "scrape failed"),
		})
	}
	c.JSON(http.StatusOK, CrawlErrorsResponse{Errors: out, RobotsBlocked: []string{}})
}

func (h *Handler) HandleOngoingCrawls(c *gin.Context) {
	jobs, err := h.Store.ListOngoingCrawls(c.Request.Context())
	if err != nil {
		writeError(c, 500, CodeUnknown, "failed to list crawls")
		return
	}
	out := make([]OngoingCrawl, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, OngoingCrawl{
			ID: j.ID.String(), TeamID: "self", URL: j.URL,
			CreatedAt: j.CreatedAt.Format(time.RFC3339), Options: j.Request,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "crawls": out})
}

func (h *Handler) HandleCrawlPreview(c *gin.Context) {
	writeError(c, 501, CodeProviderUnavailable, "crawl params preview is not available in this deployment")
}

func (h *Handler) HandleMap(c *gin.Context) {
	var req MapRequest
	if err := decodeJSON(c, &req, maxJSONBody); err != nil {
		writeDecodeError(c, err)
		return
	}
	if err := validateURL(req.URL); err != nil {
		writeBadRequest(c, err.Error())
		return
	}
	if req.Limit == 0 {
		req.Limit = 5000
	}
	if req.Limit < 1 || req.Limit > 100000 {
		writeBadRequest(c, "limit must be between 1 and 100000")
		return
	}
	if req.Sitemap != "" && req.Sitemap != "skip" && req.Sitemap != "include" && req.Sitemap != "only" {
		writeBadRequest(c, "sitemap must be skip, include, or only")
		return
	}
	opts := scraper.DefaultOptions()
	opts.Timeout = time.Duration(req.Timeout) * time.Millisecond
	if req.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	res, err := crawl.MapBFS(c.Request.Context(), req.URL, crawl.Options{
		MaxURLs: req.Limit, MaxDepth: 2, Scrape: *opts,
	}, nil, nil)
	if err != nil {
		writeError(c, 500, CodeUnknown, "map failed")
		return
	}
	links := make([]map[string]string, 0, len(res.URLs))
	for _, raw := range res.URLs {
		if req.Search != "" && !strings.Contains(strings.ToLower(raw), strings.ToLower(req.Search)) {
			continue
		}
		links = append(links, map[string]string{"url": raw})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "id": uuid.NewString(), "links": links})
}

func validateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("url must be a valid http:// or https:// URL")
	}
	return nil
}

func wireStatus(s string) string {
	switch s {
	case "queued", "processing":
		return "scraping"
	case "completed", "failed", "cancelled":
		return s
	default:
		return "scraping"
	}
}

func (h *Handler) baseURL(c *gin.Context) string {
	if h.Config.PublicBaseURL != "" {
		return strings.TrimRight(h.Config.PublicBaseURL, "/")
	}
	scheme := c.GetHeader("X-Forwarded-Proto")
	if scheme == "" {
		if c.Request.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return scheme + "://" + c.Request.Host
}

func (h *Handler) expiresAt(created time.Time) string {
	d := 24 * time.Hour
	if h.Config.DemoMode {
		d = time.Hour
	}
	return created.Add(d).UTC().Format(time.RFC3339)
}

func (h *Handler) demoMaxCrawl() int {
	if h.Config.MaxCrawlPages > 0 {
		return h.Config.MaxCrawlPages
	}
	return 25
}

func stringOr(p *string, fallback string) string {
	if p != nil && *p != "" {
		return *p
	}
	return fallback
}

func atoiDefault(s string, n int) (int, bool) {
	if s == "" {
		return n, true
	}
	var v int
	_, err := fmt.Sscanf(s, "%d", &v)
	return v, err == nil
}

func crawlPath(c *gin.Context) string {
	if strings.Contains(c.FullPath(), "batch") {
		return "batch/scrape/" + c.Param("jobId")
	}
	return "crawl/" + c.Param("jobId")
}

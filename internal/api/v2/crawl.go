package v2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
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
	if req.Sitemap == "" {
		req.Sitemap = "include"
	}
	if req.Sitemap != "skip" && req.Sitemap != "include" && req.Sitemap != "only" {
		writeBadRequest(c, "sitemap must be skip, include, or only")
		return
	}
	if req.Delay != 0 && (req.Delay < 1 || req.Delay > 60) {
		writeBadRequest(c, "delay must be between 1 and 60 seconds")
		return
	}
	if req.MaxConcurrency != 0 && req.MaxConcurrency < 1 {
		writeBadRequest(c, "maxConcurrency must be a positive integer")
		return
	}
	if req.MaxDiscoveryDepth != nil && *req.MaxDiscoveryDepth < 0 {
		writeBadRequest(c, "maxDiscoveryDepth must be non-negative")
		return
	}
	if req.MaxDepth != nil && *req.MaxDepth < 0 {
		writeBadRequest(c, "maxDepth must be non-negative")
		return
	}
	if err := validatePathPatterns(req.IncludePaths, req.ExcludePaths); err != nil {
		writeBadRequest(c, err.Error())
		return
	}
	if err := validateWebhook(req.Webhook); err != nil {
		writeBadRequest(c, err.Error())
		return
	}

	key := c.GetHeader("x-idempotency-key")
	if key != "" && len(key) > 256 {
		writeBadRequest(c, "x-idempotency-key must be at most 256 characters")
		return
	}

	job := &store.V2Job{Kind: store.KindCrawl, URL: req.URL, Request: mustJSON(req), Webhook: mustJSON(req.Webhook)}
	id, conflict, err := h.Store.CreateV2JobWithIdempotency(c.Request.Context(), job, key)
	if err != nil {
		writeError(c, 500, CodeUnknown, "failed to create crawl")
		return
	}
	if conflict {
		writeError(c, http.StatusConflict, CodeIdempotencyReused, "Idempotency key already used")
		return
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
		body := gin.H{
			"success": false, "status": "failed", "error": stringOr(job.ErrorMsg, "crawl failed"),
			"completed": completed, "total": total, "creditsUsed": 0,
			"expiresAt": h.expiresAt(job.CreatedAt), "createdAt": job.CreatedAt.UTC().Format(time.RFC3339),
			"data": []Document{},
		}
		if job.CompletedAt != nil {
			body["completedAt"] = job.CompletedAt.UTC().Format(time.RFC3339)
			if !job.CreatedAt.IsZero() {
				body["duration"] = job.CompletedAt.Sub(job.CreatedAt).Seconds()
			}
		}
		c.JSON(http.StatusOK, body)
		return
	}
	next := ""
	if skip+len(data) < completed {
		next = fmt.Sprintf("%s/v2/%s?skip=%d&limit=%d", h.baseURL(c), crawlPath(c), skip+len(data), limit)
	}
	resp := CrawlStatusResponse{
		Success: true, Status: status, Completed: completed, Total: total,
		CreditsUsed: 0, ExpiresAt: h.expiresAt(job.CreatedAt), CreatedAt: job.CreatedAt.UTC().Format(time.RFC3339), Next: next, Data: data,
	}
	if job.CompletedAt != nil {
		resp.CompletedAt = job.CompletedAt.UTC().Format(time.RFC3339)
		if !job.CreatedAt.IsZero() {
			resp.Duration = job.CompletedAt.Sub(job.CreatedAt).Seconds()
		}
		if warning := crawlWarning(job, completed); warning != "" {
			resp.Warning = warning
		}
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) HandleCrawlCancel(c *gin.Context) {
	id, ok := parseJobID(c)
	if !ok {
		return
	}
	status, err := h.Store.CancelV2Job(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
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
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Job not found"})
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
		opts := j.Request
		if len(opts) == 0 {
			opts = json.RawMessage(`{}`)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(opts, &m); err == nil {
			if _, ok := m["scrapeOptions"]; !ok {
				m["scrapeOptions"] = json.RawMessage(`{}`)
			}
			if b, err := json.Marshal(m); err == nil {
				opts = b
			}
		}
		out = append(out, OngoingCrawl{
			ID: j.ID.String(), TeamID: "self", URL: j.URL,
			CreatedAt: j.CreatedAt.Format(time.RFC3339), Options: opts,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "crawls": out})
}

func (h *Handler) HandleCrawlPreview(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"success": false, "error": "crawl params preview is not available in this deployment"})
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
	if req.Sitemap == "" {
		req.Sitemap = "include"
	}
	if req.Sitemap != "skip" && req.Sitemap != "include" && req.Sitemap != "only" {
		writeBadRequest(c, "sitemap must be skip, include, or only")
		return
	}
	if req.Timeout < 0 {
		writeBadRequest(c, "timeout must be non-negative")
		return
	}
	if err := validatePathPatterns(req.IncludePaths, req.ExcludePaths); err != nil {
		writeBadRequest(c, err.Error())
		return
	}
	includeSubdomains := true
	if req.IncludeSubdomains != nil {
		includeSubdomains = *req.IncludeSubdomains
	}
	ignoreQuery := true
	if req.IgnoreQueryParameters != nil {
		ignoreQuery = *req.IgnoreQueryParameters
	}
	opts := scraper.DefaultOptions()
	opts.Timeout = time.Duration(req.Timeout) * time.Millisecond
	if req.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	ctx := c.Request.Context()
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Millisecond)
		defer cancel()
	}
	res, err := crawl.MapBFS(ctx, req.URL, crawl.Options{
		MaxURLs: req.Limit, MaxDepth: 2, Scrape: *opts,
	}, nil, nil)
	if err != nil {
		if ctx.Err() != nil || strings.Contains(err.Error(), "timeout") {
			writeError(c, http.StatusRequestTimeout, CodeTimeout, "map timed out")
			return
		}
		writeError(c, 500, CodeUnknown, "map failed")
		return
	}
	seed, _ := url.Parse(req.URL)
	links := make([]map[string]string, 0, len(res.URLs))
	for _, raw := range res.URLs {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		if !mapHostAllowed(seed, u, includeSubdomains, req.AllowSubdomains, req.AllowExternalLinks) {
			continue
		}
		target := raw
		if ignoreQuery {
			cu := *u
			cu.RawQuery = ""
			cu.Fragment = ""
			target = cu.String()
		}
		if !pathAllowed(target, req.IncludePaths, req.ExcludePaths, false) {
			continue
		}
		if req.Search != "" && !strings.Contains(strings.ToLower(target), strings.ToLower(req.Search)) {
			continue
		}
		links = append(links, map[string]string{"url": target})
		if len(links) >= req.Limit {
			break
		}
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

func crawlWarning(job *store.V2Job, completed int) string {
	if job == nil || job.Kind != store.KindCrawl || job.Status != "completed" || completed > 1 {
		return ""
	}
	var req CrawlRequest
	if err := json.Unmarshal(job.Request, &req); err != nil {
		return ""
	}
	if req.CrawlEntireDomain {
		return ""
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return ""
	}
	if u.Path != "" && u.Path != "/" {
		return "Only 1 page was crawled. If you expected more results, try crawlEntireDomain: true to crawl sibling and parent paths on the same host."
	}
	return ""
}

func validatePathPatterns(include, exclude []string) error {
	if len(include) > 1000 || len(exclude) > 1000 {
		return fmt.Errorf("includePaths and excludePaths may contain at most 1000 patterns each")
	}
	totalCount := len(include) + len(exclude)
	if totalCount > 1000 {
		return fmt.Errorf("includePaths and excludePaths together may contain at most 1000 patterns")
	}
	totalChars := 0
	check := func(patterns []string) error {
		for _, p := range patterns {
			if len(p) > 2000 {
				return fmt.Errorf("path patterns must be at most 2000 characters")
			}
			totalChars += len(p)
			if _, err := regexp.Compile(p); err != nil {
				return fmt.Errorf("invalid path regex")
			}
		}
		return nil
	}
	if err := check(include); err != nil {
		return err
	}
	if err := check(exclude); err != nil {
		return err
	}
	if totalChars > 100000 {
		return fmt.Errorf("includePaths and excludePaths together may contain at most 100000 characters")
	}
	return nil
}

func pathAllowed(raw string, include, exclude []string, regexOnFullURL bool) bool {
	target := raw
	if u, err := url.Parse(raw); err == nil && !regexOnFullURL {
		target = u.Path
	}
	for _, p := range exclude {
		if re, err := regexp.Compile(p); err == nil && re.MatchString(target) {
			return false
		}
	}
	if len(include) == 0 {
		return true
	}
	for _, p := range include {
		if re, err := regexp.Compile(p); err == nil && re.MatchString(target) {
			return true
		}
	}
	return false
}

func mapHostAllowed(seed, u *url.URL, includeSubdomains, allowSubdomains, allowExternal bool) bool {
	if seed == nil || u == nil {
		return false
	}
	if strings.EqualFold(seed.Hostname(), u.Hostname()) {
		return true
	}
	if allowExternal {
		return true
	}
	if includeSubdomains || allowSubdomains {
		sh := strings.ToLower(seed.Hostname())
		uh := strings.ToLower(u.Hostname())
		return strings.HasSuffix(uh, "."+sh)
	}
	return false
}

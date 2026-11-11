package v2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Masralai/web-gobbler/internal/scraper"
	"github.com/Masralai/web-gobbler/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) HandleScrape(c *gin.Context) {
	var req ScrapeRequest
	if err := decodeJSON(c, &req, maxJSONBody); err != nil {
		writeDecodeError(c, err)
		return
	}
	if err := validateScrapeRequest(&req, h.Config.DemoMode, scraper.BrowserAvailable()); err != nil {
		var se scrapeValidationError
		if errors.As(err, &se) {
			if se.code == "" {
				c.AbortWithStatusJSON(se.status, gin.H{"success": false, "error": se.msg})
				return
			}
			writeError(c, se.status, se.code, se.msg)
			return
		}
		msg := err.Error()
		writeBadRequest(c, msg)
		return
	}
	now := time.Now()
	job := &store.V2Job{
		Kind:    store.KindScrape,
		URL:     req.URL,
		Request: mustJSON(req),
		Status:  "queued",
	}
	id, err := h.Store.CreateV2Job(c.Request.Context(), job)
	if err != nil {
		writeError(c, 500, CodeUnknown, "failed to create scrape")
		return
	}
	job.ID = id

	doc, err := h.scrape(c.Request.Context(), req)
	if err != nil {
		msg := err.Error()
		_ = h.Store.UpdateV2Job(c.Request.Context(), id, "failed", nil, &msg, nil, 0, int64Ptr(time.Since(now).Milliseconds()))
		status, code := statusForScrapeError(err)
		writeError(c, status, code, msg)
		return
	}
	doc.Metadata.ScrapeID = id.String()
	result, _ := json.Marshal(doc)
	_ = h.Store.UpdateV2Job(c.Request.Context(), id, "completed", result, nil, &doc.Metadata.StatusCode, 0, int64Ptr(time.Since(now).Milliseconds()))

	c.JSON(http.StatusOK, gin.H{"success": true, "data": doc, "scrape_id": id.String()})
}

func (h *Handler) HandleScrapeStatus(c *gin.Context) {
	id, ok := parseJobID(c)
	if !ok {
		return
	}
	job, err := h.Store.GetV2Job(c.Request.Context(), id)
	if err != nil {
		writeNotFound(c, "Job not found.")
		return
	}
	if job.Status != "completed" || len(job.Result) == 0 {
		if job.Status == "queued" || job.Status == "processing" {
			c.JSON(http.StatusOK, gin.H{"success": true, "status": "scraping", "scrape_id": id.String()})
			return
		}
		writeNotFound(c, "Job not found.")
		return
	}
	var doc Document
	if err := json.Unmarshal(job.Result, &doc); err != nil {
		writeError(c, 500, CodeUnknown, "failed to read scrape")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": doc})
}

func validateScrapeRequest(req *ScrapeRequest, demo, browserAvailable bool) error {
	if strings.TrimSpace(req.URL) == "" {
		return fmt.Errorf("url is required")
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("url must be a valid http:// or https:// URL")
	}
	if req.Timeout == 0 {
		req.Timeout = 30000
	}
	if req.Timeout < 1000 || req.Timeout > 120000 {
		return fmt.Errorf("timeout must be between 1000 and 120000 milliseconds")
	}
	if req.WaitFor < 0 || req.WaitFor > 60000 || req.WaitFor > req.Timeout/2 {
		return fmt.Errorf("waitFor must be between 0 and 60000 and at most half of timeout")
	}
	if req.Formats != nil && len(req.Formats) == 0 {
		return fmt.Errorf("formats must not be empty")
	}
	if req.Formats == nil {
		req.Formats = Formats{{Type: "markdown"}}
	}
	browserRequested := req.WaitFor > 0 || len(req.Actions) > 0
	for _, f := range req.Formats {
		switch f.Type {
		case "markdown", "html", "rawHtml", "links":
		case "screenshot":
			browserRequested = true
			if f.Quality != 0 && (f.Quality < 1 || f.Quality > 100) {
				return fmt.Errorf("screenshot quality must be between 1 and 100")
			}
			if f.Viewport != nil && (f.Viewport.Width < 1 || f.Viewport.Width > 7680 || f.Viewport.Height < 1 || f.Viewport.Height > 4320) {
				return fmt.Errorf("screenshot viewport must be between 1x1 and 7680x4320")
			}
		case "json":
			if demo {
				return scrapeValidationError{status: http.StatusNotImplemented, code: CodeProviderUnavailable, msg: "extract is not configured"}
			}
			if !req.jsonAllowed() {
				return fmt.Errorf("json format requires prompt or schema")
			}
		default:
			return fmt.Errorf("unsupported format %q", f.Type)
		}
	}
	if err := validateActions(req.Actions, req.WaitFor); err != nil {
		return err
	}
	if browserRequested && !browserAvailable {
		return scrapeValidationError{status: http.StatusNotImplemented, msg: "browser is not configured"}
	}
	if browserRequested && browserAvailable {
		// ponytail: execution not implemented yet; behave as unavailable rather than partial.
		return scrapeValidationError{status: http.StatusNotImplemented, msg: "browser is not configured"}
	}
	return nil
}

type scrapeValidationError struct {
	status int
	code   string
	msg    string
}

func (e scrapeValidationError) Error() string { return e.msg }

func validateActions(actions []Action, waitFor int) error {
	if len(actions) > 50 {
		return fmt.Errorf("actions must contain at most 50 entries")
	}
	totalWait := waitFor
	for _, a := range actions {
		switch a.Type {
		case "wait":
			if (a.Milliseconds > 0) == (strings.TrimSpace(a.Selector) != "") {
				return fmt.Errorf("wait action must include either milliseconds or selector, not both")
			}
			if a.Milliseconds < 0 || a.Milliseconds > 60000 {
				return fmt.Errorf("wait milliseconds must be between 0 and 60000")
			}
			totalWait += a.Milliseconds
		case "click":
			if strings.TrimSpace(a.Selector) == "" {
				return fmt.Errorf("click action requires selector")
			}
		case "screenshot":
			if a.Quality != 0 && (a.Quality < 1 || a.Quality > 100) {
				return fmt.Errorf("screenshot quality must be between 1 and 100")
			}
			if a.Viewport != nil && (a.Viewport.Width < 1 || a.Viewport.Width > 7680 || a.Viewport.Height < 1 || a.Viewport.Height > 4320) {
				return fmt.Errorf("screenshot viewport must be between 1x1 and 7680x4320")
			}
		case "write":
			if a.Text == "" {
				return fmt.Errorf("write action requires text")
			}
		case "press":
			if strings.TrimSpace(a.Key) == "" {
				return fmt.Errorf("press action requires key")
			}
		case "scroll":
			if a.Direction != "up" && a.Direction != "down" {
				return fmt.Errorf("scroll direction must be up or down")
			}
		case "executeJavascript", "pdf", "scrape":
			return fmt.Errorf("unsupported action %q", a.Type)
		default:
			return fmt.Errorf("unknown action type %q", a.Type)
		}
	}
	if totalWait > 60000 {
		return fmt.Errorf("total wait time must be at most 60000 milliseconds")
	}
	return nil
}

func (r ScrapeRequest) jsonAllowed() bool {
	for _, f := range r.Formats {
		if f.Type == "json" && (f.Prompt != "" || f.Schema != nil) {
			return true
		}
	}
	return false
}

func (h *Handler) scrape(ctx context.Context, req ScrapeRequest) (Document, error) {
	extractTypes := []string{}
	for _, f := range req.Formats {
		switch f.Type {
		case "markdown":
			extractTypes = append(extractTypes, "markdown")
		case "html":
			extractTypes = append(extractTypes, "html")
		case "rawHtml":
			extractTypes = append(extractTypes, "raw_html")
		case "links":
			extractTypes = append(extractTypes, "links")
		case "json":
			extractTypes = append(extractTypes, "markdown")
		}
	}
	opts := scraper.DefaultOptions()
	opts.Timeout = time.Duration(req.Timeout) * time.Millisecond
	opts.UserAgent = "WebGobbler/2.0"
	if len(req.Headers) > 0 {
		opts.Headers = req.Headers
	}
	opts.OnlyMainContent = req.OnlyMainContent
	opts.IncludeTags = req.IncludeTags
	opts.ExcludeTags = req.ExcludeTags
	res, err := scraper.ScrapePage(ctx, req.URL, extractTypes, opts)
	if err != nil {
		return Document{}, err
	}
	finalURL := req.URL
	if res.FinalURL != "" {
		finalURL = res.FinalURL
	}
	doc := Document{
		Markdown: res.Markdown,
		HTML:     res.HTML,
		RawHTML:  res.RawHTML,
		Links:    res.Links,
		Metadata: Metadata{
			SourceURL:   req.URL,
			URL:         finalURL,
			StatusCode:  res.HTTPStatus,
			ContentType: res.ContentType,
		},
	}
	for _, f := range req.Formats {
		if f.Type != "json" {
			continue
		}
		if !h.LLMEnabled || h.Extractor == nil || h.Config.DemoMode {
			return Document{}, fmt.Errorf("extract is not configured")
		}
		var schema json.RawMessage
		if f.Schema != nil {
			schema = mustJSON(f.Schema)
		}
		raw, err := h.Extractor.Extract(ctx, doc.Markdown, schema, f.Prompt)
		if err != nil {
			return Document{}, err
		}
		doc.JSON = raw
	}
	return doc, nil
}

func statusForScrapeError(err error) (int, string) {
	if err == nil {
		return http.StatusOK, ""
	}
	msg := err.Error()
	if strings.Contains(msg, "not configured") || strings.Contains(msg, "PROVIDER_NOT_CONFIGURED") {
		return http.StatusNotImplemented, CodeProviderUnavailable
	}
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out") {
		return http.StatusRequestTimeout, CodeTimeout
	}
	return http.StatusBadRequest, CodeBadRequest
}

func parseJobID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("jobId"))
	if err != nil {
		writeError(c, http.StatusBadRequest, CodeBadRequest, "Invalid job ID format. Job ID must be a valid UUID.")
		return uuid.Nil, false
	}
	return id, true
}

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func int64Ptr(n int64) *int64 { return &n }

package v2

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Masralai/web-gobbler/internal/queue"
	"github.com/Masralai/web-gobbler/internal/scraper"
	"github.com/Masralai/web-gobbler/internal/store"
	"github.com/gin-gonic/gin"
)

func validatePublicURL(ctx context.Context, raw string) error {
	return scraper.ValidatePublicURL(ctx, raw)
}

func (h *Handler) HandleExtract(c *gin.Context) {
	var req ExtractRequest
	if err := decodeJSON(c, &req, maxJSONBody); err != nil {
		writeDecodeError(c, err)
		return
	}
	if len(req.URLs) == 0 {
		writeBadRequest(c, "urls must contain between 1 and 10 URLs")
		return
	}
	if len(req.URLs) > 10 {
		writeBadRequest(c, "urls must contain between 1 and 10 URLs")
		return
	}
	ignoreInvalid := true
	if req.IgnoreInvalidURLs != nil {
		ignoreInvalid = *req.IgnoreInvalidURLs
	}
	valid := make([]string, 0, len(req.URLs))
	invalid := make([]string, 0)
	for _, u := range req.URLs {
		if err := validateURL(u); err != nil {
			if ignoreInvalid {
				invalid = append(invalid, u)
			} else {
				writeBadRequest(c, err.Error())
				return
			}
			continue
		}
		if err := validatePublicURL(c.Request.Context(), u); err != nil {
			if ignoreInvalid {
				invalid = append(invalid, u)
			} else {
				writeBadRequest(c, "url is not allowed: "+u)
				return
			}
			continue
		}
		valid = append(valid, u)
	}
	if len(valid) == 0 {
		writeBadRequest(c, "No valid URLs provided")
		return
	}
	req.URLs = valid
	if req.Prompt == "" && req.Schema == nil {
		writeBadRequest(c, "prompt or schema is required")
		return
	}
	if len(req.Prompt) > 10000 {
		writeBadRequest(c, "prompt must be at most 10000 characters")
		return
	}
	if req.Schema != nil {
		if b, err := json.Marshal(req.Schema); err == nil {
			var v any
			if json.Unmarshal(b, &v) == nil {
				if _, ok := v.(map[string]any); !ok {
					writeBadRequest(c, "schema must be a JSON object")
					return
				}
			}
		}
	}
	if h.Config.DemoMode || !h.LLMEnabled || h.Extractor == nil {
		writeProviderUnavailable(c, "extract is not configured")
		return
	}
	job := &store.V2Job{Kind: store.KindExtract, URL: req.URLs[0], Request: mustJSON(req)}
	id, err := h.Store.CreateV2Job(c.Request.Context(), job)
	if err != nil {
		writeError(c, 500, CodeUnknown, "failed to create extract")
		return
	}
	if err := h.Queue.PublishStream(c.Request.Context(), queue.StreamDefault, queue.StreamPayload{
		JobID: id.String(), Kind: store.KindExtract, URL: req.URLs[0],
	}); err != nil {
		writeError(c, 500, CodeUnknown, "failed to queue extract")
		return
	}
	c.Header("Deprecation", "true")
	resp := gin.H{"success": true, "id": id.String()}
	if len(invalid) > 0 {
		resp["invalidURLs"] = invalid
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) HandleExtractStatus(c *gin.Context) {
	id, ok := parseJobID(c)
	if !ok {
		return
	}
	job, err := h.Store.GetV2Job(c.Request.Context(), id)
	if err != nil {
		writeNotFound(c, "Extract job not found")
		return
	}
	status := job.Status
	if status == "queued" || status == "processing" {
		status = "processing"
	}
	expiresAt := h.expiresAt(job.CreatedAt)
	if job.CompletedAt != nil {
		expiresAt = job.CompletedAt.Add(24 * time.Hour).UTC().Format(time.RFC3339)
	}
	resp := gin.H{
		"success": status != "failed", "status": status,
		"expiresAt": expiresAt,
	}
	if status == "completed" && len(job.Result) > 0 {
		var data any
		if err := json.Unmarshal(job.Result, &data); err == nil {
			resp["data"] = data
		}
	}
	if status == "failed" {
		resp["error"] = stringOr(job.ErrorMsg, "extract failed")
	}
	c.Header("Deprecation", "true")
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) HandleParseFormats(c *gin.Context) {
	formats := []ParseFormat{
		{Format: "pdf", Kind: "document", Extensions: []string{".pdf"}, MimeTypes: []string{"application/pdf"}, Available: true},
		{Format: "docx", Kind: "document", Extensions: []string{".docx"}, MimeTypes: []string{"application/vnd.openxmlformats-officedocument.wordprocessingml.document"}, Available: true},
		{Format: "html", Kind: "document", Extensions: []string{".html", ".htm"}, MimeTypes: []string{"text/html"}, Available: true},
		{Format: "md", Kind: "document", Extensions: []string{".md"}, MimeTypes: []string{"text/markdown"}, Available: true},
		{Format: "txt", Kind: "document", Extensions: []string{".txt"}, MimeTypes: []string{"text/plain"}, Available: true},
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"formats": formats}})
}

func (h *Handler) HandleConcurrency(c *gin.Context) {
	n, err := h.Store.InFlightCount(c.Request.Context())
	if err != nil {
		writeError(c, 500, CodeUnknown, "failed to read concurrency")
		return
	}
	max := h.Config.MaxConcurrentJobs
	if max <= 0 {
		max = 5
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "concurrency": n, "maxConcurrency": max})
}

func (h *Handler) now() time.Time { return time.Now() }

package v2

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Masralai/web-gobbler/internal/queue"
	"github.com/Masralai/web-gobbler/internal/store"
	"github.com/gin-gonic/gin"
)

func (h *Handler) HandleExtract(c *gin.Context) {
	var req ExtractRequest
	if err := decodeJSON(c, &req, maxJSONBody); err != nil {
		writeDecodeError(c, err)
		return
	}
	if len(req.URLs) == 0 || len(req.URLs) > 10 {
		writeBadRequest(c, "urls must contain between 1 and 10 URLs")
		return
	}
	for _, u := range req.URLs {
		if err := validateURL(u); err != nil {
			writeBadRequest(c, err.Error())
			return
		}
	}
	if req.Prompt == "" && req.Schema == nil {
		writeBadRequest(c, "prompt or schema is required")
		return
	}
	if len(req.Prompt) > 10000 {
		writeBadRequest(c, "prompt must be at most 10000 characters")
		return
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
	c.JSON(http.StatusOK, gin.H{"success": true, "id": id.String()})
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
	resp := gin.H{
		"success": status != "failed", "status": status,
		"expiresAt": h.expiresAt(job.CreatedAt),
	}
	if status == "completed" && len(job.Result) > 0 {
		var data any
		_ = json.Unmarshal(job.Result, &data)
		resp["data"] = data
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

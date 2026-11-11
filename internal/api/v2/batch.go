package v2

import (
	"net/http"

	"github.com/Masralai/web-gobbler/internal/queue"
	"github.com/Masralai/web-gobbler/internal/scraper"
	"github.com/Masralai/web-gobbler/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) HandleBatch(c *gin.Context) {
	var req BatchRequest
	if err := decodeJSON(c, &req, maxJSONBody); err != nil {
		writeDecodeError(c, err)
		return
	}
	if len(req.URLs) == 0 {
		writeBadRequest(c, "urls must contain at least one URL")
		return
	}
	if len(req.URLs) > 10000 {
		writeBadRequest(c, "urls must contain at most 10000 URLs")
		return
	}
	if err := validateWebhook(req.Webhook); err != nil {
		writeBadRequest(c, err.Error())
		return
	}
	if req.MaxConcurrency != 0 && req.MaxConcurrency < 1 {
		writeBadRequest(c, "maxConcurrency must be a positive integer")
		return
	}
	ignoreInvalid := true
	if req.IgnoreInvalidURLs != nil {
		ignoreInvalid = *req.IgnoreInvalidURLs
	}
	valid := make([]string, 0, len(req.URLs))
	invalid := make([]string, 0)
	for _, raw := range req.URLs {
		if validateURL(raw) == nil {
			if err := scraper.ValidatePublicURL(c.Request.Context(), raw); err != nil {
				if ignoreInvalid {
					invalid = append(invalid, raw)
					continue
				}
				writeBadRequest(c, "url is not allowed: "+raw)
				return
			}
			valid = append(valid, raw)
		} else if ignoreInvalid {
			invalid = append(invalid, raw)
		} else {
			writeBadRequest(c, "invalid URL: "+raw)
			return
		}
	}
	if len(valid) == 0 {
		writeBadRequest(c, "No valid URLs provided")
		return
	}

	var parentID uuid.UUID
	appending := false
	if req.AppendToID != nil && *req.AppendToID != "" {
		id, err := uuid.Parse(*req.AppendToID)
		if err != nil {
			writeBadRequest(c, "appendToId must be a UUID")
			return
		}
		job, err := h.Store.GetV2Job(c.Request.Context(), id)
		if err != nil || job.Kind != store.KindBatch || job.Status == "cancelled" || job.Status == "failed" {
			writeBadRequest(c, "appendToId must reference an active batch")
			return
		}
		parentID = id
		appending = true
	} else {
		job := &store.V2Job{Kind: store.KindBatch, URL: valid[0], Request: mustJSON(req), Webhook: mustJSON(req.Webhook)}
		id, err := h.Store.CreateV2Job(c.Request.Context(), job)
		if err != nil {
			writeError(c, 500, CodeUnknown, "failed to create batch")
			return
		}
		parentID = id
	}

	for _, raw := range valid {
		childID, err := h.Store.CreateV2Child(c.Request.Context(), parentID, raw, 0, mustJSON(req))
		if err != nil {
			writeError(c, 500, CodeUnknown, "failed to create batch children")
			return
		}
		if err := h.Queue.PublishStream(c.Request.Context(), queue.StreamDefault, queue.StreamPayload{
			JobID: childID.String(), Kind: store.KindPage, ParentID: parentID.String(), URL: raw,
		}); err != nil {
			writeError(c, 500, CodeUnknown, "failed to queue batch")
			return
		}
	}
	if appending {
		_ = h.Store.ReopenV2Job(c.Request.Context(), parentID)
	}

	resp := gin.H{"success": true, "id": parentID.String(), "url": h.baseURL(c) + "/v2/batch/scrape/" + parentID.String()}
	if len(invalid) > 0 {
		resp["invalidURLs"] = invalid
	}
	c.JSON(http.StatusOK, resp)
}

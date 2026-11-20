package v2

import (
	"context"
	"encoding/json"

	"github.com/Masralai/web-gobbler/internal/queue"
)

func (h *Handler) dispatchOutbox(ctx context.Context) error {
	if h == nil || h.Store == nil || h.Queue == nil {
		return nil
	}
	rows, err := h.Store.ListUnsentOutbox(ctx, 100)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var p queue.StreamPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			_ = h.Store.MarkOutboxSent(ctx, r.ID) // ponytail: drop malformed row
			continue
		}
		if err := h.Queue.PublishStream(ctx, r.Stream, p); err != nil {
			return err
		}
		if err := h.Store.MarkOutboxSent(ctx, r.ID); err != nil {
			return err
		}
	}
	return nil
}

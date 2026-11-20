package v2

import (
	"context"
	"testing"
)

func TestDispatchOutboxNilDeps(t *testing.T) {
	var h *Handler
	if err := h.dispatchOutbox(context.Background()); err != nil {
		t.Fatal(err)
	}
	h = &Handler{}
	if err := h.dispatchOutbox(context.Background()); err != nil {
		t.Fatal(err)
	}
}

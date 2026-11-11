package v2

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Masralai/web-gobbler/internal/scraper"
)

func validateWebhook(w *Webhook) error {
	if w == nil {
		return nil
	}
	for k := range w.Headers {
		if strings.EqualFold(k, "x-webhook-signature") {
			return fmt.Errorf("webhook headers must not include x-webhook-signature")
		}
	}
	if strings.TrimSpace(w.URL) == "" {
		return fmt.Errorf("webhook url is required")
	}
	u, err := url.Parse(w.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("webhook url must be a valid http:// or https:// URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := scraper.ValidatePublicURL(ctx, w.URL); err != nil {
		return fmt.Errorf("webhook url is not allowed")
	}
	return nil
}

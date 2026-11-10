package v2

import (
	"fmt"
	"strings"
)

func validateWebhook(w *Webhook) error {
	if w == nil {
		return nil
	}
	for k := range w.Headers {
		if strings.EqualFold(k, "x-firecrawl-signature") {
			return fmt.Errorf("webhook headers must not include x-firecrawl-signature")
		}
	}
	return nil
}

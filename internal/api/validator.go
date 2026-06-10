package api

import (
	"fmt"
	"net/url"
	"strings"
)

var validExtractTypes = map[string]bool{
	"links":      true,
	"headers":    true,
	"paragraphs": true,
}

func validateScrapeRequest(req *ScrapeRequest) error {
	if req.URL == "" {
		return fmt.Errorf("url is required")
	}

	parsed, err := url.Parse(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("url must be a valid http:// or https:// URL")
	}

	extract := req.Extract
	if len(extract) > 0 {
		for _, t := range extract {
			if !validExtractTypes[strings.ToLower(t)] {
				return fmt.Errorf("invalid extract type %q: must be one of links, headers, paragraphs", t)
			}
		}
	}

	if req.Options != nil {
		if req.Options.TimeoutSeconds != nil {
			if *req.Options.TimeoutSeconds < 1 || *req.Options.TimeoutSeconds > 60 {
				return fmt.Errorf("timeout_seconds must be between 1 and 60")
			}
		}
		if req.Options.MaxRetries != nil {
			if *req.Options.MaxRetries < 0 || *req.Options.MaxRetries > 5 {
				return fmt.Errorf("max_retries must be between 0 and 5")
			}
		}
	}

	return nil
}

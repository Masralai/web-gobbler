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
	"markdown":   true,
	"html":       true,
	"raw_html":   true,
}

const extractTypesMsg = "links, headers, paragraphs, markdown, html, raw_html"

func validateScrapeRequest(req *ScrapeRequest) error {
	if req.URL == "" {
		return fmt.Errorf("url is required")
	}

	parsed, err := url.Parse(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("url must be a valid http:// or https:// URL")
	}

	if err := validateExtract(req.Extract); err != nil {
		return err
	}

	if req.Options != nil {
		if err := validateOptions(req.Options); err != nil {
			return err
		}
	}

	return nil
}

func validateExtract(extract []string) error {
	for _, t := range extract {
		if !validExtractTypes[strings.ToLower(t)] {
			return fmt.Errorf("invalid extract type %q: must be one of %s", t, extractTypesMsg)
		}
	}
	return nil
}

func validateOptions(opts *RequestOptions) error {
	if opts.TimeoutSeconds != nil && (*opts.TimeoutSeconds < 1 || *opts.TimeoutSeconds > 60) {
		return fmt.Errorf("timeout_seconds must be between 1 and 60")
	}
	if opts.MaxRetries != nil && (*opts.MaxRetries < 0 || *opts.MaxRetries > 5) {
		return fmt.Errorf("max_retries must be between 0 and 5")
	}
	if opts.MaxPages != nil && (*opts.MaxPages < 1 || *opts.MaxPages > 100) {
		return fmt.Errorf("max_pages must be between 1 and 100")
	}
	if opts.MaxDepth != nil && (*opts.MaxDepth < 0 || *opts.MaxDepth > 5) {
		return fmt.Errorf("max_depth must be between 0 and 5")
	}
	if opts.MaxURLs != nil && (*opts.MaxURLs < 1 || *opts.MaxURLs > 500) {
		return fmt.Errorf("max_urls must be between 1 and 500")
	}
	return nil
}

func validateCrawlRequest(req *CrawlRequest) error {
	if req.URL == "" {
		return fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("url must be a valid http:// or https:// URL")
	}
	if err := validateExtract(req.Extract); err != nil {
		return err
	}
	if req.Options != nil {
		return validateOptions(req.Options)
	}
	return nil
}

func validateMapRequest(req *MapRequest) error {
	if req.URL == "" {
		return fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("url must be a valid http:// or https:// URL")
	}
	if req.Options != nil {
		return validateOptions(req.Options)
	}
	return nil
}

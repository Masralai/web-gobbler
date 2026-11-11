package v2

import (
	"bytes"
	"encoding/json"
)

type Document struct {
	Markdown   string          `json:"markdown,omitempty"`
	HTML       string          `json:"html,omitempty"`
	RawHTML    string          `json:"rawHtml,omitempty"`
	Links      []string        `json:"links,omitempty"`
	JSON       json.RawMessage `json:"json,omitempty"`
	Screenshot string          `json:"screenshot,omitempty"`
	Actions    *ActionsOutput  `json:"actions,omitempty"`
	Metadata   Metadata        `json:"metadata"`
}

type ActionsOutput struct {
	Screenshots []string `json:"screenshots,omitempty"`
}

type Metadata struct {
	SourceURL   string `json:"sourceURL"`
	URL         string `json:"url"`
	StatusCode  int    `json:"statusCode"`
	ContentType string `json:"contentType,omitempty"`
	ScrapeID    string `json:"scrapeId,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Language    string `json:"language,omitempty"`
	Keywords    string `json:"keywords,omitempty"`
	Robots      string `json:"robots,omitempty"`
	OgImage     string `json:"ogImage,omitempty"`
	Favicon     string `json:"favicon,omitempty"`
}

type ErrorResponse struct {
	Success bool            `json:"success"`
	Code    string          `json:"code,omitempty"`
	Error   string          `json:"error"`
	Details json.RawMessage `json:"details,omitempty"`
}

type ScrapeOptions struct {
	Formats         Formats           `json:"formats,omitempty"`
	OnlyMainContent *bool             `json:"onlyMainContent,omitempty"`
	IncludeTags     []string          `json:"includeTags,omitempty"`
	ExcludeTags     []string          `json:"excludeTags,omitempty"`
	Timeout         int               `json:"timeout,omitempty"`
	WaitFor         int               `json:"waitFor,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Location        json.RawMessage   `json:"location,omitempty"`
	MaxAge          int               `json:"maxAge,omitempty"`
	Origin          string            `json:"origin,omitempty"`
	Integration     *string           `json:"integration,omitempty"`
	Actions         []Action          `json:"actions,omitempty"`
}

type ScrapeRequest struct {
	URL string `json:"url"`
	ScrapeOptions
}

type Format struct {
	Type     string    `json:"type"`
	Prompt   string    `json:"prompt,omitempty"`
	Schema   any       `json:"schema,omitempty"`
	FullPage bool      `json:"fullPage,omitempty"`
	Quality  int       `json:"quality,omitempty"`
	Viewport *Viewport `json:"viewport,omitempty"`
}

type Viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type Formats []Format

func (f *Formats) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*f = append(*f, Format{Type: s})
		return nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	out := make([]Format, 0, len(list))
	for _, raw := range list {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		if raw[0] == '"' {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return err
			}
			out = append(out, Format{Type: s})
			continue
		}
		var format Format
		if err := strictUnmarshal(raw, &format); err != nil {
			return err
		}
		out = append(out, format)
	}
	*f = out
	return nil
}

func (f Formats) Types() []string {
	out := make([]string, 0, len(f))
	for _, x := range f {
		out = append(out, x.Type)
	}
	return out
}

type Action struct {
	Type         string    `json:"type"`
	Milliseconds int       `json:"milliseconds,omitempty"`
	Selector     string    `json:"selector,omitempty"`
	All          bool      `json:"all,omitempty"`
	FullPage     bool      `json:"fullPage,omitempty"`
	Quality      int       `json:"quality,omitempty"`
	Viewport     *Viewport `json:"viewport,omitempty"`
	Text         string    `json:"text,omitempty"`
	Key          string    `json:"key,omitempty"`
	Direction    string    `json:"direction,omitempty"`
}

type Webhook struct {
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Events   []string          `json:"events,omitempty"`
}

func (w *Webhook) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*w = Webhook{URL: s}
		return nil
	}
	type alias Webhook
	var a alias
	if err := strictUnmarshal(data, &a); err != nil {
		return err
	}
	*w = Webhook(a)
	return nil
}

type CrawlRequest struct {
	URL                    string         `json:"url"`
	Limit                  *int           `json:"limit,omitempty"`
	MaxDiscoveryDepth      *int           `json:"maxDiscoveryDepth,omitempty"`
	MaxDepth               *int           `json:"maxDepth,omitempty"`
	IncludePaths           []string       `json:"includePaths,omitempty"`
	ExcludePaths           []string       `json:"excludePaths,omitempty"`
	AllowSubdomains        bool           `json:"allowSubdomains,omitempty"`
	CrawlEntireDomain      bool           `json:"crawlEntireDomain,omitempty"`
	AllowExternalLinks     bool           `json:"allowExternalLinks,omitempty"`
	IgnoreRobotsTxt        bool           `json:"ignoreRobotsTxt,omitempty"`
	RobotsUserAgent        string         `json:"robotsUserAgent,omitempty"`
	Sitemap                string         `json:"sitemap,omitempty"`
	DeduplicateSimilarURLs *bool          `json:"deduplicateSimilarURLs,omitempty"`
	IgnoreQueryParameters  *bool          `json:"ignoreQueryParameters,omitempty"`
	RegexOnFullURL         *bool          `json:"regexOnFullURL,omitempty"`
	Delay                  int            `json:"delay,omitempty"`
	MaxConcurrency         int            `json:"maxConcurrency,omitempty"`
	ScrapeOptions          *ScrapeOptions `json:"scrapeOptions,omitempty"`
	Webhook                *Webhook       `json:"webhook,omitempty"`
	Origin                 string         `json:"origin,omitempty"`
	Integration            *string        `json:"integration,omitempty"`
}

type BatchRequest struct {
	URLs              []string       `json:"urls"`
	ScrapeOptions     *ScrapeOptions `json:"scrapeOptions,omitempty"`
	Webhook           *Webhook       `json:"webhook,omitempty"`
	IgnoreInvalidURLs *bool          `json:"ignoreInvalidURLs,omitempty"`
	AppendToID        *string        `json:"appendToId,omitempty"`
	MaxConcurrency    int            `json:"maxConcurrency,omitempty"`
}

type MapRequest struct {
	URL                   string            `json:"url"`
	Limit                 int               `json:"limit,omitempty"`
	Search                string            `json:"search,omitempty"`
	IncludeSubdomains     *bool             `json:"includeSubdomains,omitempty"`
	IgnoreQueryParameters *bool             `json:"ignoreQueryParameters,omitempty"`
	Sitemap               string            `json:"sitemap,omitempty"`
	Timeout               int               `json:"timeout,omitempty"`
	IncludePaths          []string          `json:"includePaths,omitempty"`
	ExcludePaths          []string          `json:"excludePaths,omitempty"`
	AllowExternalLinks    bool              `json:"allowExternalLinks,omitempty"`
	AllowSubdomains       bool              `json:"allowSubdomains,omitempty"`
	Headers               map[string]string `json:"headers,omitempty"`
}

type SearchRequest struct {
	Query             string          `json:"query"`
	Limit             int             `json:"limit,omitempty"`
	Sources           []string        `json:"sources,omitempty"`
	ScrapeOptions     *ScrapeOptions  `json:"scrapeOptions,omitempty"`
	Timeout           int             `json:"timeout,omitempty"`
	IncludeDomains    []string        `json:"includeDomains,omitempty"`
	ExcludeDomains    []string        `json:"excludeDomains,omitempty"`
	Lang              string          `json:"lang,omitempty"`
	Country           string          `json:"country,omitempty"`
	Location          json.RawMessage `json:"location,omitempty"`
	Tbs               string          `json:"tbs,omitempty"`
	Filter            json.RawMessage `json:"filter,omitempty"`
	Safe              json.RawMessage `json:"safe,omitempty"`
	Categories        []string        `json:"categories,omitempty"`
	Enterprise        json.RawMessage `json:"enterprise,omitempty"`
	IgnoreInvalidURLs json.RawMessage `json:"ignoreInvalidURLs,omitempty"`
	AsyncScraping     json.RawMessage `json:"asyncScraping,omitempty"`
}

type ExtractRequest struct {
	URLs              []string       `json:"urls"`
	Prompt            string         `json:"prompt,omitempty"`
	Schema            any            `json:"schema,omitempty"`
	ScrapeOptions     *ScrapeOptions `json:"scrapeOptions,omitempty"`
	IgnoreInvalidURLs *bool          `json:"ignoreInvalidURLs,omitempty"`
}

type CrawlStatusResponse struct {
	Success     bool       `json:"success"`
	Status      string     `json:"status"`
	Completed   int        `json:"completed"`
	Total       int        `json:"total"`
	CreditsUsed int        `json:"creditsUsed"`
	ExpiresAt   string     `json:"expiresAt"`
	CreatedAt   string     `json:"createdAt,omitempty"`
	CompletedAt string     `json:"completedAt,omitempty"`
	Duration    float64    `json:"duration,omitempty"`
	Next        string     `json:"next,omitempty"`
	Data        []Document `json:"data"`
	Warning     string     `json:"warning,omitempty"`
}

type CrawlError struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp,omitempty"`
	URL       string `json:"url"`
	Code      string `json:"code,omitempty"`
	Error     string `json:"error"`
}

type CrawlErrorsResponse struct {
	Errors        []CrawlError `json:"errors"`
	RobotsBlocked []string     `json:"robotsBlocked"`
}

type OngoingCrawl struct {
	ID        string          `json:"id"`
	TeamID    string          `json:"teamId"`
	URL       string          `json:"url"`
	CreatedAt string          `json:"created_at"`
	Options   json.RawMessage `json:"options"`
}

type ParseFormat struct {
	Format     string   `json:"format"`
	Kind       string   `json:"kind"`
	Extensions []string `json:"extensions"`
	MimeTypes  []string `json:"mimeTypes"`
	Available  bool     `json:"available"`
}

func DefaultScrapeOptions() ScrapeOptions {
	return ScrapeOptions{Formats: Formats{{Type: "markdown"}}}
}

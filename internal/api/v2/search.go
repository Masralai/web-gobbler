package v2

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type searxResponse struct {
	Results []struct {
		URL     string  `json:"url"`
		Title   string  `json:"title"`
		Content string  `json:"content"`
		Score   float64 `json:"score"`
	} `json:"results"`
}

type webResult struct {
	URL         string          `json:"url"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Markdown    string          `json:"markdown,omitempty"`
	HTML        string          `json:"html,omitempty"`
	RawHTML     string          `json:"rawHtml,omitempty"`
	Links       []string        `json:"links,omitempty"`
	JSON        json.RawMessage `json:"json,omitempty"`
}

func (h *Handler) HandleSearch(c *gin.Context) {
	var req SearchRequest
	if c.Request.Method == http.MethodGet {
		req.Query = c.Query("query")
		if q := c.Query("q"); q != "" && req.Query == "" {
			req.Query = q
		}
		req.Limit, _ = strconv.Atoi(c.DefaultQuery("limit", "10"))
	} else if err := decodeJSON(c, &req, maxJSONBody); err != nil {
		writeDecodeError(c, err)
		return
	}
	if len(req.Query) < 1 || len(req.Query) > 400 {
		writeBadRequest(c, "query must be between 1 and 400 characters")
		return
	}
	if req.Limit == 0 {
		req.Limit = 10
	}
	if req.Limit < 1 || req.Limit > 100 {
		writeBadRequest(c, "limit must be between 1 and 100")
		return
	}
	if len(req.Sources) > 0 {
		for _, s := range req.Sources {
			if s != "web" {
				writeBadRequest(c, "only the web source is supported")
				return
			}
		}
	}
	if req.ScrapeOptions != nil {
		for _, f := range req.ScrapeOptions.Formats {
			switch f.Type {
			case "markdown", "html", "rawHtml", "links":
			default:
				writeBadRequest(c, "search scrapeOptions formats may contain only markdown, html, rawHtml, and links")
				return
			}
		}
	}
	if h.Config.SearXNGEndpoint == "" {
		writeProviderUnavailable(c, "search is not configured")
		return
	}
	if req.Timeout <= 0 {
		req.Timeout = 60000
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(req.Timeout)*time.Millisecond)
	defer cancel()
	results, err := h.search(ctx, req.Query)
	if err != nil {
		writeProviderUnavailable(c, "search is not configured: "+err.Error())
		return
	}
	out := make([]webResult, 0, req.Limit)
	for _, r := range results {
		if len(out) >= req.Limit {
			break
		}
		item := webResult{URL: r.URL, Title: r.Title, Description: r.Content}
		if req.ScrapeOptions != nil {
			doc, err := h.scrape(ctx, ScrapeRequest{URL: r.URL, ScrapeOptions: *req.ScrapeOptions})
			if err == nil {
				item.Markdown = doc.Markdown
				item.HTML = doc.HTML
				item.RawHTML = doc.RawHTML
				item.Links = doc.Links
				item.JSON = doc.JSON
			}
		}
		out = append(out, item)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true, "data": gin.H{"web": out}, "creditsUsed": 0, "id": newUUIDv7(),
	})
}

func (h *Handler) search(ctx context.Context, query string) ([]struct {
	URL     string  `json:"url"`
	Title   string  `json:"title"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}, error) {
	endpoint := h.Config.SearXNGEndpoint
	if u, err := url.Parse(endpoint); err == nil {
		q := u.Query()
		q.Set("q", query)
		q.Set("format", "json")
		q.Set("pageno", "1")
		u.RawQuery = q.Encode()
		endpoint = u.String()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("SearXNG format=json is disabled; add json to search.formats in settings.yml")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("searxng returned %d", res.StatusCode)
	}
	var parsed searxResponse
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := parsed.Results[:0]
	for _, r := range parsed.Results {
		if r.URL == "" || seen[r.URL] {
			continue
		}
		seen[r.URL] = true
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}


func newUUIDv7() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	ns := uint64(time.Now().UnixMilli())
	b[0] = byte(ns >> 40)
	b[1] = byte(ns >> 32)
	b[2] = byte(ns >> 24)
	b[3] = byte(ns >> 16)
	b[4] = byte(ns >> 8)
	b[5] = byte(ns)
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return uuid.UUID(b).String()
}

package v2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestFormatsUnmarshal(t *testing.T) {
	var f Formats
	if err := f.UnmarshalJSON([]byte(`["markdown",{"type":"json","prompt":"name"}]`)); err != nil {
		t.Fatal(err)
	}
	if len(f) != 2 || f[0].Type != "markdown" || f[1].Type != "json" || f[1].Prompt != "name" {
		t.Fatalf("unexpected formats: %#v", f)
	}
}

func TestStrictUnmarshalRejectsUnknown(t *testing.T) {
	var req ScrapeRequest
	err := strictUnmarshal([]byte(`{"url":"https://example.com","nope":true}`), &req)
	if err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestValidateScrapeRequestDefaultsAndBounds(t *testing.T) {
	req := ScrapeRequest{URL: "https://example.com"}
	if err := validateScrapeRequest(&req, false, false, false); err != nil {
		t.Fatal(err)
	}
	if req.Timeout != 30000 || len(req.Formats) != 1 || req.Formats[0].Type != "markdown" {
		t.Fatalf("defaults not applied: %#v", req)
	}

	req = ScrapeRequest{URL: "https://example.com", ScrapeOptions: ScrapeOptions{Timeout: 30000, WaitFor: 20000}}
	if err := validateScrapeRequest(&req, false, false, false); err == nil {
		t.Fatal("expected waitFor bound error")
	}

	req = ScrapeRequest{URL: "https://example.com", ScrapeOptions: ScrapeOptions{Formats: Formats{{Type: "nope"}}}}
	if err := validateScrapeRequest(&req, false, false, false); err == nil {
		t.Fatal("expected unsupported format error")
	}

	req = ScrapeRequest{URL: "https://example.com", ScrapeOptions: ScrapeOptions{Formats: Formats{{Type: "json", Prompt: "x"}}}}
	if err := validateScrapeRequest(&req, false, false, false); err == nil {
		t.Fatal("expected json provider unavailable error")
	}
	if err := validateScrapeRequest(&req, false, true, false); err != nil {
		t.Fatalf("expected json format allowed when llm enabled, got %v", err)
	}
}

func TestValidateActions(t *testing.T) {
	if err := validateActions([]Action{{Type: "executeJavascript"}}, 0); err == nil || err.Error() == "" {
		t.Fatal("expected unsupported action error")
	}
	if err := validateActions(make([]Action, 51), 0); err == nil {
		t.Fatal("expected action count error")
	}
	if err := validateActions([]Action{{Type: "wait", Milliseconds: 30000}, {Type: "wait", Milliseconds: 31000}}, 0); err == nil {
		t.Fatal("expected total wait error")
	}
	if err := validateActions([]Action{{Type: "click"}}, 0); err == nil {
		t.Fatal("expected click selector error")
	}
}

func TestWireStatus(t *testing.T) {
	cases := map[string]string{
		"queued": "scraping", "processing": "scraping",
		"completed": "completed", "failed": "failed", "cancelled": "cancelled",
	}
	for in, want := range cases {
		if got := wireStatus(in); got != want {
			t.Fatalf("wireStatus(%q)=%q want %q", in, got, want)
		}
	}
}

func TestValidatePathPatterns(t *testing.T) {
	if err := validatePathPatterns([]string{"^/docs"}, []string{"^/admin"}); err != nil {
		t.Fatal(err)
	}
	if err := validatePathPatterns([]string{"("}, nil); err == nil {
		t.Fatal("expected invalid regex error")
	}
	tooMany := make([]string, 1001)
	for i := range tooMany {
		tooMany[i] = "a"
	}
	if err := validatePathPatterns(tooMany, nil); err == nil {
		t.Fatal("expected count limit error")
	}
}

func TestUnsupportedSearchField(t *testing.T) {
	req := SearchRequest{IncludeDomains: []string{"example.com"}}
	if got := unsupportedSearchField(req); got != "includeDomains" {
		t.Fatalf("got %q", got)
	}
	req = SearchRequest{Lang: "en"}
	if got := unsupportedSearchField(req); got != "lang" {
		t.Fatalf("got %q", got)
	}
	req = SearchRequest{}
	if got := unsupportedSearchField(req); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateExtractSchema(t *testing.T) {
	if err := validateExtractSchema(map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}); err != nil {
		t.Fatalf("expected valid schema, got %v", err)
	}
	if err := validateExtractSchema("not-object"); err == nil {
		t.Fatal("expected non-object schema error")
	}
	if err := validateExtractSchema(map[string]any{"type": 123}); err == nil {
		t.Fatal("expected invalid schema error")
	}
}

func TestValidateScrapeOptions(t *testing.T) {
	opts := &ScrapeOptions{Timeout: 0}
	if err := validateScrapeOptions(opts, false, false, false); err != nil {
		t.Fatal(err)
	}
	if opts.Timeout != 30000 || len(opts.Formats) != 1 || opts.Formats[0].Type != "markdown" {
		t.Fatalf("defaults not applied: %#v", opts)
	}

	opts = &ScrapeOptions{Formats: Formats{{Type: "json", Prompt: "x"}}}
	if err := validateScrapeOptions(opts, false, false, false); err == nil {
		t.Fatal("expected provider unavailable for json scrapeOptions")
	}

	opts = &ScrapeOptions{WaitFor: 100}
	if err := validateScrapeOptions(opts, false, false, false); err == nil {
		t.Fatal("expected browser unavailable for waitFor scrapeOptions")
	}
}

func TestNewUUIDv7(t *testing.T) {
	id := newUUIDv7()
	u, err := uuid.Parse(id)
	if err != nil {
		t.Fatal(err)
	}
	if u.Version() != 7 {
		t.Fatalf("version=%d want 7", u.Version())
	}
	if u.Variant() != uuid.RFC4122 {
		t.Fatalf("variant=%v", u.Variant())
	}
}

func TestHandleParseFormats(t *testing.T) { // AC-006b
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v2/parse/formats", nil)

	(&Handler{}).HandleParseFormats(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Formats []ParseFormat `json:"formats"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success || len(resp.Data.Formats) == 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	for _, f := range resp.Data.Formats {
		if f.Format == "" || f.Kind == "" || len(f.Extensions) == 0 || len(f.MimeTypes) == 0 {
			t.Fatalf("missing required fields: %+v", f)
		}
	}
}

func TestHandleMapRejectsSitemapAlways(t *testing.T) { // AC-002c
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"url":"https://example.com","sitemap":"always"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v2/map", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	(&Handler{}).HandleMap(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleSearchRejectsUnsupportedSource(t *testing.T) { // AC-004b
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"query":"test","sources":["news"]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v2/search", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	(&Handler{Config: Config{}}).HandleSearch(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

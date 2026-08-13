package extract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_Extract_Mock(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("missing auth")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": `{"title":"Hello"}`}},
			},
		})
	}))
	defer ts.Close()

	c := NewClient(Config{APIKey: "test", BaseURL: ts.URL, Model: "mock"}, ts.Client())
	out, err := c.Extract(context.Background(), "# Hello\n\nWorld", json.RawMessage(`{"type":"object"}`), "get title")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["title"] != "Hello" {
		t.Fatalf("got %v", m)
	}
}

func TestFromEnv_Disabled(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	if FromEnv().Enabled() {
		t.Fatal("expected disabled")
	}
}

func TestFromEnv_GeminiDefaults(t *testing.T) {
	t.Setenv("LLM_API_KEY", "test-key")
	t.Setenv("LLM_BASE_URL", "")
	t.Setenv("LLM_MODEL", "")
	cfg := FromEnv()
	if cfg.BaseURL != "https://generativelanguage.googleapis.com/v1beta/openai" {
		t.Fatalf("BaseURL=%q", cfg.BaseURL)
	}
	if cfg.Model != "gemini-2.5-flash" {
		t.Fatalf("Model=%q", cfg.Model)
	}
}

func TestClient_RequiresKey(t *testing.T) {
	c := NewClient(Config{}, nil)
	_, err := c.Extract(context.Background(), "x", nil, "p")
	if err == nil {
		t.Fatal("expected error")
	}
}

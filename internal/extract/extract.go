// Package extract runs optional one-shot LLM schema extraction over scraped markdown (P5).
// It is not an agent: one OpenAI-compatible chat completion per call, no tools/search.
package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const maxMarkdownChars = 100_000
const maxPromptChars = 4_000

// Extractor turns markdown + schema/prompt into structured JSON.
type Extractor interface {
	Extract(ctx context.Context, markdown string, schema json.RawMessage, prompt string) (json.RawMessage, error)
}

// Config holds LLM endpoint settings from the environment.
type Config struct {
	APIKey  string
	BaseURL string
	Model   string
}

// FromEnv loads config. Empty APIKey means feature is off.
// Defaults target Gemini's OpenAI-compatible endpoint (free-tier friendly).
// Override with LLM_BASE_URL / LLM_MODEL for other OpenAI-compatible providers.
func FromEnv() Config {
	base := os.Getenv("LLM_BASE_URL")
	if base == "" {
		base = "https://generativelanguage.googleapis.com/v1beta/openai"
	}
	model := os.Getenv("LLM_MODEL")
	if model == "" {
		model = "gemini-2.5-flash"
	}
	return Config{
		APIKey:  os.Getenv("LLM_API_KEY"),
		BaseURL: strings.TrimRight(base, "/"),
		Model:   model,
	}
}

// Enabled reports whether LLM extract is configured.
func (c Config) Enabled() bool {
	return c.APIKey != ""
}

// Client is an OpenAI-compatible chat completions client.
type Client struct {
	cfg    Config
	client *http.Client
}

// NewClient builds a Client. HTTPClient may be nil.
func NewClient(cfg Config, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{cfg: cfg, client: httpClient}
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	ResponseFormat *respFormat   `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type respFormat struct {
	Type       string          `json:"type"`
	JSONSchema *jsonSchemaWrap `json:"json_schema,omitempty"`
}

type jsonSchemaWrap struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Extract performs one completion. Truncates oversized markdown.
func (c *Client) Extract(ctx context.Context, markdown string, schema json.RawMessage, prompt string) (json.RawMessage, error) {
	if !c.cfg.Enabled() {
		return nil, fmt.Errorf("LLM extract not configured")
	}
	if prompt == "" && len(schema) == 0 {
		return nil, fmt.Errorf("schema or prompt is required")
	}
	if len(prompt) > maxPromptChars {
		return nil, fmt.Errorf("prompt too long (max %d chars)", maxPromptChars)
	}
	md := markdown
	if len(md) > maxMarkdownChars {
		md = md[:maxMarkdownChars]
	}

	sys := "Extract structured data from the markdown. Reply with JSON only."
	user := md
	if prompt != "" {
		user = prompt + "\n\n---\n\n" + md
	}

	reqBody := chatRequest{
		Model: c.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: sys},
			{Role: "user", Content: user},
		},
	}
	if len(schema) > 0 {
		reqBody.ResponseFormat = &respFormat{
			Type: "json_schema",
			JSONSchema: &jsonSchemaWrap{
				Name:   "extract",
				Strict: false,
				Schema: schema,
			},
		}
	} else {
		reqBody.ResponseFormat = &respFormat{Type: "json_object"}
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm request failed")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("llm invalid response")
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("llm error")
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("llm empty response")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if !json.Valid([]byte(content)) {
		return nil, fmt.Errorf("llm returned non-json")
	}
	return json.RawMessage(content), nil
}

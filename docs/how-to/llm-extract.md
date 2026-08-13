# How to use LLM extract (P5)

Optional **one-shot** structured extract over **already scraped markdown**. Not an agent (no tools, browse, or search).

## Requirements

Set on the **API** process:

| Env | Required | Default |
|-----|----------|---------|
| `LLM_API_KEY` | yes | unset → feature off (`501`) |
| `LLM_BASE_URL` | no | `https://generativelanguage.googleapis.com/v1beta/openai` (Gemini) |
| `LLM_MODEL` | no | `gemini-2.5-flash` |

Defaults use Gemini’s free-tier OpenAI-compatible API. Override `LLM_BASE_URL` / `LLM_MODEL` for OpenAI or any other compatible server.

Without a key, scrape/crawl/map are unchanged.

## Flow

1. Scrape (or crawl) with `extract: ["markdown"]`.
2. Wait until job `completed`.
3. Call extract:

```bash
curl -X POST http://localhost:8080/api/v1/jobs/$JOB_ID/extract \
  -H "Content-Type: application/json" \
  -d '{
    "prompt": "Extract the page title",
    "schema": {
      "type": "object",
      "properties": { "title": { "type": "string" } },
      "required": ["title"]
    }
  }'
```

Scrape response:

```json
{ "job_id": "...", "extracted": { "title": "Example Domain" } }
```

Crawl: one completion **per page** → `pages: [{ "url", "extracted" }]`. Markdown on the job is never deleted if extract fails.

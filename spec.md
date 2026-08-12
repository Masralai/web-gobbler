# Spec — LLM-ready scrape/crawl (optional LLM extract)

**Status:** P0–P5 implemented (2026-07-24)  
**Replaces:** `aim.md` (product direction expanded into an executable phase spec)  
**Built system:** [`prd.md`](prd.md)  
**Ops/how-to:** [`README.md`](README.md), [`docs/how-to/run.md`](docs/how-to/run.md), [`docs/`](docs/)

---

## 1. North star

A **self-hosted, deterministic** scrape/crawl API that returns **LLM-ready page text** on the existing async job shell (Gin → Redis → workers → Postgres).

```
URL  →  HTTP fetch  →  HTML parse  →  markdown / HTML / links  →  job API  →  caller’s app
                                                                    └─ optional P5: schema extract via configured LLM
```

**Default path never calls a model.** P0–P4 stay deterministic. Optional **P5** can run a single completion over already-fetched markdown when the operator configures an API key. This service **never** runs an autonomous agent (no tool loops, browse planning, or web search).

**Makeshift Firecrawl (our meaning):** Firecrawl-like scrape/crawl *outputs*; optional schema extract; no agent, search, credits, or SaaS.

---

## 2. Glossary

| Term | Meaning | In this project? |
|------|---------|------------------|
| **LLM extract** | After scrape, a model fills a schema/prompt from markdown | **Optional (P5)** — off by default; needs `LLM_API_KEY` |
| **Agent** | Model plans multi-step browse/search to answer a goal | **No** — caller supplies URLs or we crawl a seed |
| **Markdown scrape** | HTML → main content → markdown string | **Yes (P0)** |
| **Crawl** | Seed URL → same-origin BFS → many pages, one job | **Yes (P2)** |
| **Map** | Discover URLs only, no bodies | **Optional (P3)** |
| **Browser fetch** | Headless Chromium for JS-heavy pages | **Maybe (P4)** — still no LLM unless P5 also enabled |

---

## 3. Current baseline

Already done per `prd.md` / handover **and** this spec’s phases:

| Capability | Status |
|------------|--------|
| Job API (`POST /scrape`, poll, list, cancel), `/health`, `/metrics` | Done |
| Redis queue, workers, retries, rate limits | Done |
| Postgres, Prometheus/Grafana, Docker, Terraform, CI | Done |
| Extract: `links`, `headers`, `paragraphs`, **`markdown`**, **`html`**, **`raw_html`** | Done (P0–P1) |
| `POST /crawl`, `POST /map` | Done (P2–P3) |
| `options.render_js` + compose profile `browser` | Done (P4) |
| `POST /jobs/:id/extract` (optional LLM) | Done (P5) |

**Verified:** unit + integration tests; [`scripts/feature-matrix.sh`](scripts/feature-matrix.sh) 12/12 against local Compose.

---

## 4. Non-goals (all phases)

- Autonomous agents, tool loops, or “answer this from the web”  
- Web search / Q&A over the internet  
- Multi-tenant auth, credits, billing  
- Official SDKs / Firecrawl wire-compat  
- Browser-by-default scraping  
- Rewriting the job shell “for Firecrawl shape”  
- **LLM extract in P0–P4** — deferred to optional **P5**; off by default; no agent  

---
---

## 5. Global rules for coding agents

Apply on every phase:

| Rule | Detail |
|------|--------|
| One phase per PR / session unless asked otherwise | Do not start P2 while P0 is open |
| Extend, don’t fork | Prefer new extract types / fields on `scraper.Result` and existing workers |
| Keep SSRF + body limits | Private IPs, redirect checks, max body — never relax for “just this site” |
| Tests before claiming done | Unit for scraper/API; integration when store/queue/API contracts change |
| Docs in the same phase | README curl + tutorial touch when API behavior changes |
| Ponytail | Shortest working diff; no speculative abstractions |

### Skills cheat-sheet (attach when implementing)

| Skill | When |
|-------|------|
| `ponytail` | Always — keep diffs small |
| `golang-pro` | Any Go change |
| `web-scraping` | Fetch/parse/extract/crawl/browser |
| `api-and-interface-design` | Request/response contracts, new endpoints |
| `tdd` | Non-trivial scraper/crawl logic |
| `golang-security` / `security-and-hardening` | SSRF, redirects, HTML sanitization edges, browser surface, LLM API key handling (P5) |
| `documentation-writer` | README / `docs/` / this spec status updates |
| `create-readme` | README structure when examples grow |
| `supabase-postgres-best-practices` | Schema/index changes for crawl results |
| `docker-expert` | P4 browser image/runtime |
| `requesting-code-review` | End of each phase before merge |
| `systematic-debugging` / `diagnose` | Only when a phase is stuck on a real bug |
| `spec-driven-development` | If a phase needs a tighter sub-spec before code |

---

## 6. Phases (detailed)

Execute **in order**. Each phase has: goal, work steps, files, API/data changes, acceptance, skills, exit criteria.

---

### Phase P0 — Markdown scrape

**Goal:** One URL in → clean markdown out via existing poll API.

**Depends on:** baseline only.

#### Work steps

1. **Design result shape** — Add `Markdown string \`json:"markdown,omitempty"\`` to [`scraper.Result`](internal/scraper/scraper.go). Allow extract type `"markdown"`. Default extract remains `["links"]` (do not change default to markdown without a docs callout).
2. **Main-content isolation** — From parsed `goquery.Document`, prefer `article` / `main` / role=main; else body. Strip `script`, `style`, `nav`, `footer`, `noscript`.
3. **HTML → markdown** — Deterministic converter (stdlib-friendly or one small dep). Preserve headings, links, lists, code blocks enough for RAG paste.
4. **Validator** — Add `markdown` to `validExtractTypes`; update error string.
5. **API meta** — Optional `markdown_length` in `JobMeta` if cheap; don’t invent counts that lie.
6. **Tests** — Table tests: fixture HTML → expected markdown substrings; empty body; script-only page.
7. **Docs** — README + [`docs/tutorials/getting-started.md`](docs/tutorials/getting-started.md) + [`docs/how-to/add-extract-type.md`](docs/how-to/add-extract-type.md) example for markdown.

#### Primary files

- [`internal/scraper/scraper.go`](internal/scraper/scraper.go), `scraper_test.go` (new helpers ok: e.g. `markdown.go`)
- [`internal/api/validator.go`](internal/api/validator.go), [`types.go`](internal/api/types.go), handlers meta if needed
- README / docs as above

#### API example

```json
POST /api/v1/scrape
{ "url": "https://example.com", "extract": ["markdown"] }
```

Completed job `results.markdown` is a non-empty string for example.com.

#### Acceptance

- [ ] `extract: ["markdown"]` accepted; unknown types still 400  
- [ ] Unit tests cover converter + strip noise  
- [ ] Manual: compose up → scrape example.com → poll → markdown readable  
- [ ] Existing links/headers/paragraphs tests still green  
- [ ] No new network calls to model providers  

#### Agent skills (P0)

`ponytail`, `golang-pro`, `web-scraping`, `api-and-interface-design`, `tdd`, `golang-security`, `documentation-writer`

#### Exit

Mark P0 done in §8 changelog. **Do not** open crawl work in the same change.

---

### Phase P1 — Formats (HTML + multi-extract)

**Goal:** One fetch serves multiple formats without a second HTTP round-trip.

**Depends on:** P0.

#### Work steps

1. Add extract types `html` (cleaned main HTML) and optionally `raw_html` (truncated body, size-capped — reuse `MaxBodySize`).
2. Ensure `ScrapePage` parses once; fill only requested fields.
3. Allow `extract: ["markdown", "links"]` in one job; document combo.
4. Cap stored HTML size in DB/JSON (reject or truncate with clear meta flag — prefer truncate + `truncated: true` in meta over OOM).
5. Tests for multi-extract and size cap.
6. Docs: formats table in README.

#### Primary files

- `internal/scraper/*`, `internal/api/validator.go`, `types.go`, store JSON already flexible via JSONB
- Docs

#### Acceptance

- [ ] Multi-extract returns all requested fields from one scrape  
- [ ] Huge pages cannot blow worker memory beyond existing body limit  
- [ ] Backward compatible with P0 and legacy extract types  

#### Agent skills (P1)

`ponytail`, `golang-pro`, `api-and-interface-design`, `web-scraping`, `tdd`, `documentation-writer`, `golang-security`

#### Exit

Changelog §8. Prefer stopping here if crawl is not needed yet.

---

### Phase P2 — Crawl

**Goal:** Seed URL → same-origin multi-page job; poll one `job_id` for all pages.

**Depends on:** P0 (markdown). P1 recommended.

#### Work steps

1. **API contract (commit to this shape):**
   - `POST /api/v1/crawl` with body:
     ```json
     {
       "url": "https://example.com",
       "extract": ["markdown"],
       "options": {
         "timeout_seconds": 15,
         "max_retries": 3,
         "max_pages": 10,
         "max_depth": 2
       }
     }
     ```
   - Response: same 202 job envelope as scrape (`job_id`, `poll_url`).
   - Defaults: `max_pages=10`, `max_depth=2`, hard caps (e.g. max_pages ≤ 100, max_depth ≤ 5) at validation.
2. **Data model** — Extend job result JSONB to hold either single-page `Result` or crawl payload:
   ```json
   {
     "pages": [
       { "url": "...", "depth": 0, "markdown": "...", "links": [], "http_status": 200 }
     ],
     "pages_crawled": 3,
     "pages_skipped": 1
   }
   ```
   Prefer one JSONB `result` shape with optional `pages` array rather than a new table unless listing pages becomes painful — **ponytail: JSONB first**.
3. **Worker** — New process path (or job kind flag on payload): BFS queue in-memory per job; normalize URLs (strip fragment); same-host only; respect robots.txt **only if cheap** — default skip robots in P2 unless already trivial (document choice).
4. **Rate limit** — Reuse per-domain limiter across pages of the crawl.
5. **Cancel** — Existing `ClaimJob` / cancel: if job cancelled mid-crawl, stop enqueueing further pages and mark failed/cancelled.
6. **Metrics** — Optional counter `scraper_crawl_pages_total`; don’t block phase on Grafana art.
7. **Tests** — `httptest` multi-page fixture site; depth/page caps; cross-origin links ignored.
8. **Docs** — new how-to or tutorial section for crawl; update architecture/job-lifecycle briefly.

#### Primary files

- `internal/api/` (handler, routes, validator, types)
- `internal/queue` payload fields for crawl options
- `cmd/worker` crawl loop
- `internal/scraper` URL normalize helpers
- migrations only if JSONB-only proves insufficient (avoid if possible)
- docs

#### Acceptance

- [ ] Crawl of a 3-page static fixture returns ≤ `max_pages` pages, depths respected  
- [ ] Off-origin links not fetched  
- [ ] Cancel stops further work  
- [ ] Scrape endpoint behavior unchanged  
- [ ] Integration or unit coverage for BFS caps  

#### Agent skills (P2)

`ponytail`, `golang-pro`, `web-scraping`, `api-and-interface-design`, `tdd`, `documentation-writer`, `golang-security`, `supabase-postgres-best-practices` (only if schema changes), `requesting-code-review`

#### Exit

Changelog §8. Map (P3) is optional.

---

### Phase P3 — Map (optional)

**Goal:** Return discovered URLs for a seed without storing full page bodies.

**Depends on:** P2 frontier logic.

#### Work steps

1. `POST /api/v1/map` — options: `max_urls`, `max_depth`; response job with `results.urls: []string`.
2. Reuse crawl discovery; skip markdown/HTML extraction (HEAD or light GET + link parse only).
3. Tests + short docs. **Skip entire phase** if P2 crawl listing already covers discovery needs.

#### Agent skills (P3)

`ponytail`, `golang-pro`, `api-and-interface-design`, `web-scraping`, `tdd`, `documentation-writer`

#### Exit

Changelog or explicit “skipped — crawl sufficient.”

---

### Phase P4 — Browser fetch (maybe never)

**Goal:** Optional headless path for pages that are empty without JS. **Still no LLM.**

**Depends on:** Real failed targets under P0/P2 (document URLs). Do not start on speculation.

#### Work steps

1. Prove 2+ important URLs fail static scrape; capture evidence in docs.
2. Add opt-in `options.render_js: true` (scrape/crawl) — default false.
3. Sidecar or library (e.g. chromedp / playwright) behind interface `Fetcher`; static HTTP remains default.
4. Docker: separate worker tag or optional compose profile — **distroless static worker must not gain Chromium by default**.
5. Timeouts, concurrency caps (browser is heavy), SSRF still enforced on navigations.
6. Docs: ops cost, memory, when to enable.

#### Agent skills (P4)

`ponytail`, `golang-pro`, `web-scraping`, `docker-expert`, `golang-security`, `security-and-hardening`, `documentation-writer`, `requesting-code-review`

#### Exit

Only with evidence + ops notes. Otherwise leave marked `deferred` in §8.

---

### Phase P5 — Optional LLM extract

**Goal:** Opt-in structured JSON from **already-fetched markdown** via one model completion. Not an agent.

**Depends on:** P0 (markdown on the job). Crawl pages (P2): extract **per page** only in P5.

**Positioning:** Pipeline stays scrape → markdown. P5 is a **second step**: send stored markdown + JSON Schema and/or prompt to an operator-configured OpenAI-compatible endpoint. Default deploy (no `LLM_API_KEY`) never calls a model; P0–P2 product success does **not** require P5.

#### API (committed)

Primary: extract on a **completed** job so scrape/crawl stay useful without keys.

```http
POST /api/v1/jobs/:id/extract
Content-Type: application/json

{
  "schema": { "type": "object", "properties": { "title": { "type": "string" } } },
  "prompt": "Extract the article title"
}
```

- Job must be `completed` with markdown available (single-page `results.markdown` or crawl `results.pages[].markdown`).
- Response: `200` with `extracted` JSON (scrape) or `pages[].extracted` (crawl); original markdown **unchanged**.
- If `LLM_API_KEY` unset → `501 Not Implemented` (or `400` on validate with clear error). Prefer **501** when the feature is compiled in but unconfigured.

Env:

| Var | Required for P5 | Default |
|-----|-----------------|---------|
| `LLM_API_KEY` | yes | unset (feature off) |
| `LLM_BASE_URL` | no | provider default / `https://api.openai.com/v1` |
| `LLM_MODEL` | no | document a single default in README when implementing |

#### Guards

- Off unless key configured; never log API keys or full prompts at ERROR.
- Size/token cap on markdown sent to the model (truncate with meta flag if over cap).
- Crawl: **one completion per page**, not one giant concat (document this).
- No browsing, no tool loop, no search — **one** chat/completions-style call per extract unit.
- Model/HTTP failure: return error on the extract call; **do not** delete stored markdown. Do not flip a successful scrape job to `failed` solely because extract failed (extract is a separate request).

#### Work steps

1. Add `internal/extract` (or similar) with an `Extractor` interface and one OpenAI-compatible HTTP client.
2. Wire `POST /api/v1/jobs/:id/extract` in [`internal/api`](internal/api); load job markdown from store.
3. Validate schema/prompt bounds (max prompt length, schema size).
4. Unit tests with `httptest` mock LLM; assert no call when key unset.
5. Docs: env vars, example schema, explicit “not an agent” note.

#### Primary files

- New: `internal/extract/` (client + interface)
- [`internal/api/handlers.go`](internal/api/handlers.go), routes, types, validator
- README / how-to for LLM extract
- Compose/Terraform: optional secret for `LLM_API_KEY` (no key in images)

#### Acceptance

- [ ] Without `LLM_API_KEY`, scrape/crawl unchanged; extract endpoint returns 501  
- [ ] With key + mock server, schema-shaped JSON returned; markdown preserved  
- [ ] No multi-step tool/agent behavior  
- [ ] Secrets not present in error responses or ERROR logs  

#### Agent skills (P5)

`ponytail`, `golang-pro`, `api-and-interface-design`, `tdd`, `golang-security`, `security-and-hardening`, `documentation-writer`, `requesting-code-review`

Thin HTTP client only — do not pull in a full chat-app skill/stack.

#### Exit

Changelog §8. Core “LLM-ready fetch” story remains P0–P2 without this phase.

---

## 7. Suggested coding-agent session template

Paste when starting a phase:

```text
Implement spec.md phase P<N> only.
Attach skills: <list from that phase>.
Constraints: ponytail; don’t start later phases.
  P0–P4: no LLM calls; no agents.
  P5 only: optional single-shot extract; no agents/tool loops; off without LLM_API_KEY.
Acceptance: checklist in spec.md for P<N>.
Update README/docs if API changed; append §8 changelog line.
```

---

## 8. Changelog (phase completion log)

| Phase | Status | Date | Notes |
|-------|--------|------|-------|
| P0 Markdown | **done** | 2026-07-24 | `extract: markdown`; [`internal/scraper/markdown.go`](internal/scraper/markdown.go) |
| P1 Formats | **done** | 2026-07-24 | `html`, `raw_html` + 512KiB truncate / `truncated` |
| P2 Crawl | **done** | 2026-07-24 | `POST /crawl`; [`internal/crawl`](internal/crawl); robots.txt skipped |
| P3 Map | **done** | 2026-07-24 | `POST /map` → `results.urls` |
| P4 Browser | **done** | 2026-07-24 | `render_js`; Fetcher + chromedp; profile `browser` |
| P5 LLM extract | **done** | 2026-07-24 | `POST /jobs/:id/extract`; [`internal/extract`](internal/extract); off without key |

### Progress notes

- Default path remains deterministic (no model calls) unless `LLM_API_KEY` is set on the API.
- Distroless worker stays Chrome-free; JS rendering uses `docker compose --profile browser`.
- Local Compose raises `API_RATE_LIMIT` / `API_RATE_BURST` so feature-matrix polling is not blocked.
- Schema still applied on API/worker boot via `store.Migrate` (no separate migrate container).

---

## 9. Doc map

| Doc | Role |
|-----|------|
| [`prd.md`](prd.md) | Original distributed scraper system |
| [`spec.md`](spec.md) | Product bridge + phases + this progress log |
| [`docs/how-to/run.md`](docs/how-to/run.md) | **How to run the app** (Compose, curls, optional browser/LLM, tests) |
| [`docs/tutorials/getting-started.md`](docs/tutorials/getting-started.md) | First markdown scrape walkthrough |
| [`docs/how-to/crawl.md`](docs/how-to/crawl.md) | Crawl + map |
| [`docs/how-to/browser-render.md`](docs/how-to/browser-render.md) | P4 JS rendering |
| [`docs/how-to/llm-extract.md`](docs/how-to/llm-extract.md) | P5 extract |
| [`docs/how-to/deploy-aws.md`](docs/how-to/deploy-aws.md) | AWS / Terraform |
| [`README.md`](README.md) | Overview + API reference |
| [`handover.md`](handover.md) | Historical baseline build iterations |
| [`scripts/feature-matrix.sh`](scripts/feature-matrix.sh) | Live smoke matrix against a running API |

**Overall success (met):** `docker compose up` → submit URL → poll → usable markdown (and crawl/map). Optional P4/P5 as documented.

**P5:** `POST /jobs/:id/extract` when `LLM_API_KEY` is configured — structured JSON beside markdown, still no autonomous agent.

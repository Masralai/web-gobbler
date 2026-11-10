#!/usr/bin/env bash
set -euo pipefail

BASE="${WEB_GOBBLER_URL:-http://localhost:8080}"
KEY="${WEB_GOBBLER_API_KEY:-}"

headers=(-H "Content-Type: application/json")
if [[ -n "$KEY" ]]; then
  headers+=(-H "Authorization: Bearer $KEY")
fi

curl -fsS -X POST "$BASE/v2/scrape" "${headers[@]}" \
  -d '{"url":"https://example.com","formats":["markdown","links"]}' >/dev/null

curl -fsS -X POST "$BASE/v2/crawl" "${headers[@]}" \
  -d '{"url":"https://example.com","limit":2,"scrapeOptions":{"formats":["markdown"]}}' >/dev/null

echo "seeded demo"

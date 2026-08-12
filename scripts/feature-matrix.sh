#!/usr/bin/env bash
# Feature matrix against a running API (default http://localhost:8080).
set -uo pipefail
BASE="${1:-http://localhost:8080}"
pass=0
fail=0

check() {
  local name="$1"
  shift
  if "$@"; then
    echo "PASS  $name"
    pass=$((pass+1))
  else
    echo "FAIL  $name"
    fail=$((fail+1))
  fi
}

wait_job() {
  local id="$1" expect="$2" tries="${3:-40}"
  local body status i
  for i in $(seq 1 "$tries"); do
    body=$(curl -sf "$BASE/api/v1/jobs/$id" || true)
    status=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status',''))" 2>/dev/null || echo "")
    if [ "$status" = "completed" ] || [ "$status" = "failed" ]; then
      echo "$body"
      return 0
    fi
    sleep 1
  done
  echo "$body"
  return 1
}

echo "=== Feature matrix @ $BASE ==="

check "health" bash -c "curl -sf '$BASE/health' | grep -q status"
check "health_v1" bash -c "curl -sf '$BASE/api/v1/health' | grep -q db"

JOB=$(curl -sf -X POST "$BASE/api/v1/scrape" -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com","extract":["markdown","links"]}' || true)
ID=$(echo "$JOB" | python3 -c "import sys,json; print(json.load(sys.stdin)['job_id'])" 2>/dev/null || true)
check "scrape_accepted" test -n "${ID:-}"

BODY=$(wait_job "$ID" completed 30 || true)
check "scrape_markdown" bash -c "echo '$BODY' | python3 -c \"import sys,json; r=json.load(sys.stdin); assert r.get('status')=='completed' and len(r.get('results',{}).get('markdown') or '')>10\""

JOB=$(curl -sf -X POST "$BASE/api/v1/scrape" -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com","extract":["html","links"]}' || true)
ID=$(echo "$JOB" | python3 -c "import sys,json; print(json.load(sys.stdin)['job_id'])" 2>/dev/null || true)
BODY=$(wait_job "$ID" completed 30 || true)
check "scrape_html_links" bash -c "echo '$BODY' | python3 -c \"import sys,json; r=json.load(sys.stdin)['results']; assert r.get('html') and r.get('links') is not None\""

JOB=$(curl -sf -X POST "$BASE/api/v1/crawl" -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com","extract":["markdown"],"options":{"max_pages":2,"max_depth":1}}' || true)
ID=$(echo "$JOB" | python3 -c "import sys,json; print(json.load(sys.stdin)['job_id'])" 2>/dev/null || true)
BODY=$(wait_job "$ID" completed 45 || true)
check "crawl_pages" bash -c "echo '$BODY' | python3 -c \"import sys,json; r=json.load(sys.stdin)['results']; assert r.get('pages_crawled',0)>=1 and r.get('pages')\""

JOB=$(curl -sf -X POST "$BASE/api/v1/map" -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com","options":{"max_urls":10,"max_depth":1}}' || true)
ID=$(echo "$JOB" | python3 -c "import sys,json; print(json.load(sys.stdin)['job_id'])" 2>/dev/null || true)
BODY=$(wait_job "$ID" completed 45 || true)
check "map_urls" bash -c "echo '$BODY' | python3 -c \"import sys,json; r=json.load(sys.stdin)['results']; assert isinstance(r.get('urls'), list) and len(r['urls'])>=1\""

JOB=$(curl -sf -X POST "$BASE/api/v1/scrape" -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com","extract":["links"]}' || true)
ID=$(echo "$JOB" | python3 -c "import sys,json; print(json.load(sys.stdin)['job_id'])" 2>/dev/null || true)
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X DELETE "$BASE/api/v1/jobs/$ID" || true)
check "cancel_or_conflict" bash -c "test '$CODE' = '200' -o '$CODE' = '409'"
check "list_jobs" bash -c "curl -sf '$BASE/api/v1/jobs?limit=5' | grep -q jobs"

CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/v1/jobs/$ID/extract" \
  -H 'Content-Type: application/json' -d '{"prompt":"title"}' || true)
check "extract_501_without_key" test "$CODE" = "501"

CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/v1/scrape" \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com","extract":["markdown"],"options":{"render_js":true,"timeout_seconds":15}}' || true)
check "render_js_accepted" test "$CODE" = "202"

check "metrics" bash -c "curl -sf '$BASE/metrics' | grep -qE 'scraper_jobs_total|go_'"

echo "=== $pass passed, $fail failed ==="
exit "$fail"

#!/usr/bin/env bash
# Feature matrix against a running API (default http://localhost:8080), v2 surface.
set -uo pipefail
BASE="${1:-http://localhost:8080}"
AUTH_HEADER=()
if [ -n "${API_KEY:-}" ]; then AUTH_HEADER=(-H "Authorization: Bearer $API_KEY"); fi
pass=0
fail=0

check() {
  local name="$1"; shift
  if "$@"; then echo "PASS  $name"; pass=$((pass+1)); else echo "FAIL  $name"; fail=$((fail+1)); fi
}

wait_v2() {
  local path="$1" tries="${2:-60}" body status
  for _ in $(seq 1 "$tries"); do
    body=$(curl -sf "${AUTH_HEADER[@]}" "$BASE$path" || true)
    status=$(echo "$body" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status',''))" 2>/dev/null || echo "")
    case "$status" in completed|failed|cancelled) echo "$body"; return 0 ;; esac
    sleep 1
  done
  echo "$body"; return 1
}

echo "=== Feature matrix @ $BASE ==="
check "health" bash -c "curl -sf '$BASE/health' | grep -q status"
check "health_v1" bash -c "curl -sf '$BASE/api/v1/health' | grep -q db"

BODY=$(curl -sf "${AUTH_HEADER[@]}" -X POST "$BASE/v2/scrape" -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com","formats":["markdown","links"]}' || true)
check "scrape_ok" bash -c "echo '$BODY' | python3 -c \"import sys,json; r=json.load(sys.stdin); assert r.get('success') and len((r.get('data') or {}).get('markdown') or '')>5\""

BODY=$(curl -sf "${AUTH_HEADER[@]}" -X POST "$BASE/v2/map" -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com","limit":10}' || true)
check "map_ok" bash -c "echo '$BODY' | python3 -c \"import sys,json; r=json.load(sys.stdin); assert r.get('success') and isinstance(r.get('links'), list)\""

JOB=$(curl -sf "${AUTH_HEADER[@]}" -X POST "$BASE/v2/crawl" -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com","limit":1}' || true)
ID=$(echo "$JOB" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || true)
check "crawl_accepted" test -n "$ID"
BODY=$(wait_v2 "/v2/crawl/$ID" 60 || true)
check "crawl_terminal" bash -c "echo '$BODY' | python3 -c \"import sys,json; r=json.load(sys.stdin); assert r.get('status') in ('completed','failed','cancelled')\""

JOB=$(curl -sf "${AUTH_HEADER[@]}" -X POST "$BASE/v2/batch/scrape" -H 'Content-Type: application/json' \
  -d '{"urls":["https://example.com"]}' || true)
ID=$(echo "$JOB" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || true)
check "batch_accepted" test -n "$ID"
BODY=$(wait_v2 "/v2/batch/scrape/$ID" 60 || true)
check "batch_terminal" bash -c "echo '$BODY' | python3 -c \"import sys,json; r=json.load(sys.stdin); assert r.get('status') in ('completed','failed','cancelled')\""

check "parse_formats" bash -c "curl -sf '${AUTH_HEADER[@]}' '$BASE/v2/parse/formats' | python3 -c \"import sys,json; r=json.load(sys.stdin); assert r.get('success') and r['data']['formats']\""
check "concurrency" bash -c "curl -sf '${AUTH_HEADER[@]}' '$BASE/v2/concurrency-check' | python3 -c \"import sys,json; r=json.load(sys.stdin); assert r.get('success') and 'maxConcurrency' in r\""
check "interact_501" bash -c "test \$(curl -s -o /dev/null -w '%{http_code}' '${AUTH_HEADER[@]}' -X POST '$BASE/v2/scrape/00000000-0000-0000-0000-000000000000/interact') = '501'"
check "metrics" bash -c "curl -sf '$BASE/metrics' | grep -qE 'scraper_jobs_total|go_'"

echo "=== $pass passed, $fail failed ==="
exit "$fail"

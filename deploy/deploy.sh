#!/usr/bin/env bash
set -euo pipefail

: "${DEPLOY_HOST:?set DEPLOY_HOST}"
: "${DEPLOY_USER:=ubuntu}"
: "${DEPLOY_PATH:=/opt/web-gobbler}"
: "${ENV_FILE:=.env.production}"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "missing $ENV_FILE" >&2
  exit 1
fi

ssh "$DEPLOY_USER@$DEPLOY_HOST" "mkdir -p '$DEPLOY_PATH'"
scp "$ENV_FILE" "$DEPLOY_USER@$DEPLOY_HOST:$DEPLOY_PATH/.env"
scp -r deploy/docker-compose.prod.yml deploy/Caddyfile deploy/searxng "$DEPLOY_USER@$DEPLOY_HOST:$DEPLOY_PATH/"

ssh "$DEPLOY_USER@$DEPLOY_HOST" "cd '$DEPLOY_PATH' && chmod 600 .env && docker compose -f docker-compose.prod.yml pull && docker compose -f docker-compose.prod.yml run --rm migrate && docker compose -f docker-compose.prod.yml up -d"

curl -fsS "https://${DOMAIN:-localhost}/health" >/dev/null
echo "deployed"

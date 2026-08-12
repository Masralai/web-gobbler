# Specification — Floci Local AWS Sandbox for web-gobbler

**Status:** Draft v2 · **Owner:** web-gobbler · **Target:** local Windows (PowerShell 5.1) + Docker Desktop

## 1. Overview

The real AWS deployment costs ~$60–100/mo, which is too much for practice. This spec replaces that cost with a **free, local AWS-shaped sandbox** built on [Floci](https://floci.io) (`floci/floci:latest`, MIT, LocalStack-compatible, port 4566).

The actual objective is **using AWS infrastructure without paying AWS**: exercise Terraform, ECR, ECS, RDS, ElastiCache against real AWS wire APIs at $0. This is a *practice* environment, not a public deployment.

To maximize learning value, the default stance is **prod-fidelity**: run the real Terraform at `localhost:4566` and downgrade only the attributes Floci genuinely cannot honor — not pre-downgrade to a compose-like shape.

## 2. Goals / Non-goals

**Goals**
- Provision the full stack via one Terraform module targeting `http://localhost:4566` for $0.
- Real engines where Floci gives fidelity: Postgres (RDS), Redis/Valkey (ElastiCache), Docker tasks (ECS), image registry (ECR).
- One source of truth: a single `deploy_mode = "aws" | "floci"` variable; default mode stays byte-identical to current prod.
- One-command bootstrap: `scripts/floci-up.ps1`.
- **Learning parity**: the sandbox applies the same resource graph as prod (VPC → SG → RDS → ElastiCache → ECR → ECS → ALB), degrading only where Floci cannot emulate.

**Non-goals**
- Public hosting, real AWS account usage, cost recovery.
- Micro-fidelity of AWS networking internals (awsvpc ENI wiring, awslogs driver) — these are the negotiated downgrades.
- CI/CD changes (`.github/workflows/` is empty; skipped).
- Sandbox observability (Prometheus/Grafana out of scope for the sandbox flow).

## 3. Environment & Prerequisites

- Windows, PowerShell 5.1+, Docker Desktop (Docker Engine) running, Terraform >= 1.6, `docker compose` v2.
- Floci needs the Docker socket mounted to orchestrate the real containers it backs (RDS/ElastiCache/ECS/ECR).

## 4. Architecture

```
┌────────────────── host (localhost) ───────────────────────────┐
│  scripts/floci-up.ps1                                         │
│    │  AWS_ENDPOINT_URL=http://localhost:4566  (dummy creds)    │
│    ▼                                                           │
│  Terraform ──terraform/ (deploy_mode=floci)──►  Floci :4566     │
│    VPC/SG/RDS/ElastiCache/ECR/ECS/ALB                 │        │
│    ┌──────────────┬──────────────────┬───────────────┐         │
│    ▼              ▼                  ▼               │         │
│  registry:2     postgres:16        valkey            │         │
│  (ECR)          (RDS)           (ElastiCache)         │         │
│                  ECS task containers                  │         │
│  api  ──►  ALB (if honored) else localhost:8080       │
│  worker ─► consumes same RDS + ElastiCache             │
└──────────────────────────────────────────────────────────────────┘
```

### 4.1 Data plane
- App Postgres + Redis **come from Floci-emulated RDS + ElastiCache** (real containers), not the compose `postgres`/`redis` services.
- API reachable via ALB if Floci's ELB v2 honors the wiring; otherwise directly via bridge `hostPort: 8080` (negotiated fallback, section 4.2).

### 4.2 Fidelity strategy — attempt prod shape first, degrade on rejection

Order of attack per resource group:
1. Keep the prod attribute **as-is**; run `terraform apply` against Floci.
2. If Floci errors or returns unusable values, degrade **that attribute only** by the narrowest sane change.
3. Record each negotiated downgrade so the sandbox's deviation from prod stays *known and small*.

| Resource / attribute (prod) | Attempt (as-is) | Negotiated fallback (only if rejected) |
|---|---|---|
| VPC / subnets / IGW / SG / route tables | as-is (Floci EC2/ECS backs VPC) | none |
| `aws_db_instance` (postgres 16, snapshot, encryption) | as-is | drop `final_snapshot` / `storage_encrypted`; tune instance-class fields Floci ignores |
| `aws_elasticache_replication_group` (redis, auth, transit encryption) | as-is | drop `auth_token` / `transit_encryption_enabled` (ACL auth discovered at runtime) |
| `aws_ecr_repository` x2 | as-is (real `registry:2`) | none |
| `aws_ecs_cluster` + task definitions | as-is (FARGATE / awsvpc attempt) | `network_mode = "bridge"`, `requires_compatibilities` / `launch_type` omitted |
| task logs | `awslogs` → CloudWatch | `json-file` driver |
| config injection | `secrets` from SecretsManager | inline `environment` vars |
| ALB + target group + listener | as-is (`load_balancer` block) | drop the block; expose API via `hostPort: 8080` |
| worker auto-scaling / SNS alarms | as-is | omit (Floci stores policies inert) |
| state backend | S3 | local state for `deploy_mode=floci` |

## 5. File Changes

### 5.1 `docker-compose.yml` — add `floci` service
```yaml
  floci:
    image: floci/floci:latest
    profiles: ["sandbox"]
    ports: ["4566:4566"]
    volumes:
      - ${DOCKER_SOCK:-/var/run/docker.sock}:/var/run/docker.sock
      - floci_data:/app/data
    environment:
      FLOCI_STORAGE_MODE: persistent
      FLOCI_DEFAULT_REGION: us-east-1
      FLOCI_HOSTNAME: floci
    healthcheck:
      test: ["CMD-SHELL", "curl -sf http://localhost:4566/_localstack/health"]
      interval: 5s
      timeout: 3s
      retries: 30
```
`volumes:` adds `floci_data:`. `profiles: ["sandbox"]` keeps normal `docker compose up -d` (local dev) untouched.

### 5.2 Terraform module — parameterize the existing `terraform/` (no duplicate module)
Changes inside `terraform/` (default behavior stays identical; prod mode preserved):

- **`main.tf`** — AWS provider made conditional on `deploy_mode`:
  - `aws` (default): current settings — `region = var.aws_region`, S3 backend, real credentials.
  - `floci`: `region = "us-east-1"`, dummy `access_key`/`secret_key` (`test`/`test`), `skip_credentials_validation`, `skip_requesting_account_id`, `skip_metadata_api_check`, and `endpoints { ec2/ecs/ecr/elasticache/rds/iam/logs/sts = "http://localhost:4566" }`.
- **`variables.tf`** — add `deploy_mode` (`"aws"` default), `floci_endpoint` (default `http://localhost:4566`), fixed sandbox DB creds (`goscrape_admin` / `devpass`).
- **`ecs.tf`** — task definitions and services use `count`/`dynamic` so Fargate/awsvpc/awslogs/SecretsManager apply under `aws`, and the section-4.2 fallbacks apply under `floci`. ALB block conditionally attached.
- **`rds.tf` / `elasticache.tf`** — snapshot/encryption/auth attributes conditional on `deploy_mode`.
- **`outputs.tf`** — add `rds_host/port`, `redis_host/port`, `api_url` (ALB DNS in aws mode; `http://localhost:8080` in floci mode).
- **State**: use a committed `backend.floci.hcl` (local) swapped in during init for `deploy_mode=floci`.

> This modifies `terraform/` (unavoidable for a single source of truth). Because `deploy_mode` defaults to `"aws"`, prod applies remain byte-identical.

### 5.3 Script `scripts/floci-up.ps1` (idempotent, parameterized)

| Step | Action | Failure handling |
|---|---|---|
| Preflight | verify Docker + compose; port available | abort |
| Start | `docker compose --profile sandbox up -d floci` | abort |
| Ready | poll `GET /_localstack/health` (max 120s) | abort on timeout |
| Env | set `AWS_ENDPOINT_URL` / `ACCESS_KEY` / `SECRET` / `REGION` | — |
| Apply | `terraform -chdir=terraform init -backend-config=backend.floci.hcl` + `plan` + `apply -auto-approve -var deploy_mode=floci` | abort |
| Push | build api + worker, tag `localhost:4566/goscrape-*:latest`, `docker push` | warn on failure |
| Discover | RDS/Redis host+port from terraform outputs and `aws rds describe-db-instances` / `aws elasticache describe-replication-groups` (`--endpoint-url http://localhost:4566`) | abort if empty |
| Migrate | apply `migrations/000001_create_jobs_table.up.sql` into the discovered RDS via `psql` (or pipe through `docker exec -i <postgres-container> psql -U ... -d goscrape` via `docker ps` heuristic) | abort |
| Health | poll `GET http://localhost:8080/api/v1/health` until 200 | report failure |
| Smoke (optional) | `-SmokeTest`: POST a `/api/v1/scrape` job, poll to `completed` | report failure |
| Report | print URLs / creds / endpoints + teardown command | — |

### 5.4 `.gitignore`
Add: `.floci-data/`, `terraform/backend.floci.hcl`, `terraform/*.tfplan`. Remove the `*.md` glob (so docs + this spec are trackable).

### 5.5 `README.md`
New "Floci sandbox (local AWS, $0)" section: prerequisites, `powershell -ExecutionPolicy Bypass -File scripts/floci-up.ps1`, what it provisions, teardown (`docker compose --profile sandbox down -v`), explicit local-only note, link to the negotiated-downgrade table. Update project-structure tree with `scripts/` and `docs/`.

## 6. Connection Strings (sandbox)
- `DATABASE_URL=postgresql://goscrape_admin:devpass@localhost:<rds_port>/goscrape`
- `REDIS_URL=redis://localhost:<redis_port>` (token appended if Floci enforces ACL auth — risk 8.3)

## 7. Acceptance Criteria
1. `scripts/floci-up.ps1` runs green end-to-end on a clean machine (Docker Desktop, no AWS creds).
2. `terraform apply -var deploy_mode=floci` is idempotent (re-run shows no diff).
3. `POST /api/v1/scrape` reaches `completed`; `GET /api/v1/jobs/:id` returns results.
4. `GET http://localhost:8080/api/v1/health` shows `{"status":"ok","db":"ok","redis":"ok"}`.
5. Prod untouched: `terraform plan -var deploy_mode=aws` still matches today's prod plan and `git diff` on `terraform/` shows only additive `deploy_mode` logic.
6. Normal dev flow `docker compose up -d` unchanged.
7. `docker compose --profile sandbox down -v` removes all sandbox containers + volumes.
8. The negotiated-downgrade table (section 4.2) records every deviation from prod with rationale.

## 8. Risk Register & Mitigations

| # | Risk | Mitigation |
|---|---|---|
| 8.1 | Floci rejects some `aws_db_instance` / ElastiCache attributes | keep attr in prod; omit only where apply errors under `deploy_mode=floci` |
| 8.2 | ECS task can't pull `localhost:4566/...` from sibling registry | fallback: run api/worker as sandbox-profile compose services wired to the same RDS/Redis |
| 8.3 | ElastiCache enforces Redis ACL auth | set/read fixed dev token; discover via `describe-replication-groups` |
| 8.4 | RDS/Redis ports are dynamic | always discover via outputs, never hardcode |
| 8.5 | Windows Docker socket shim | `${DOCKER_SOCK:-/var/run/docker.sock}` mapping |
| 8.6 | Migration hits the wrong Postgres container | `docker ps` filter + `describe-db-instances` cross-check; `-Pg` override |
| 8.7 | persistent mode keeps stale state | teardown `-v`; `floci stop` resets |
| 8.8 | Single module drifts between modes | `deploy_mode=aws` default + downgrade table keeps deviations small and auditable |

## 9. Out of Scope / Deferred
- Public reachability, real-AWS deploy, CI/CD, sandbox monitoring dashboards, Floci CLI installer (script uses Docker directly), external load testing against the sandbox.
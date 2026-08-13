# PRD — Floci Local AWS Sandbox for web-gobbler

**Status:** Proposed · **Owner:** web-gobbler · **Source spec:** [`docs/FLOCI_SANDBOX.md`](FLOCI_SANDBOX.md) · **Target:** local Linux/macOS (bash) + Docker

---

## Problem Statement

Deploying web-gobbler to real AWS costs roughly **$60–100/month**, which is far too much for a practice environment. The actual objective is to learn and exercise AWS infrastructure — Terraform, ECR, ECS, RDS, ElastiCache — against real AWS wire APIs without paying for AWS. Today there is no way to run the full production resource graph locally for $0, so anyone learning the stack is either paying for AWS or skipping the infrastructure practice entirely.

The current production Terraform module (`terraform/`) is hard-wired to a real AWS region, an S3 state backend, and real credentials. There is no local path that exercises the same resource graph (VPC → SG → RDS → ElastiCache → ECR → ECS → ALB) cheaply.

## Solution

Provide a **free, local AWS-shaped sandbox** built on [Floci](https://floci.io) (`floci/floci:latest`, MIT, LocalStack-compatible, port 4566). The sandbox runs the **real production Terraform module** against `localhost:4566` and the real container engines Floci backs (Postgres for RDS, Redis/Valkey for ElastiCache, Docker tasks for ECS, `registry:2` for ECR) — at **$0**.

The default stance is **prod-fidelity**: attempt the prod attributes as-is first, and degrade only the specific attributes Floci genuinely cannot honor. The deviation from prod stays *known and small* via a recorded negotiated-downgrade table.

The whole environment is brought up with a **single command**: `scripts/floci-up.sh` — preflight, start Floci, poll health, terraform init/apply in `deploy_mode=floci`, push images, discover endpoints, run migrations, health-check the API, and print a report.

## User Stories

1. As a developer practicing AWS, I want to provision the full web-gobbler stack (VPC → SG → RDS → ElastiCache → ECR → ECS → ALB) against a local Floci endpoint, so that I exercise the real resource graph without paying AWS.
2. As a developer, I want the real Postgres (RDS) and Redis/Valkey (ElastiCache) engines to back the app, so that my local sandbox behaves like production rather than a compose mock.
3. As a developer, I want to run the **same** Terraform module for both prod and sandbox, so that there is one source of truth instead of two drifting configurations.
4. As a developer, I want a single `deploy_mode = "aws" | "floci"` variable to select the target, so that switching environments is trivial and auditable.
5. As a developer, I want `deploy_mode` to default to `"aws"`, so that the current production applies remain byte-identical and the sandbox never changes prod by accident.
6. As a developer, I want a one-command bootstrap (`scripts/floci-up.sh`) that takes a clean machine from nothing to a running, migrated sandbox, so that setup is repeatable and scriptable.
7. As a developer, I want the script to be idempotent, so that re-running it produces no drift or duplicate resources.
8. As a developer, I want RDS/Redis host and port discovered at runtime via terraform outputs, so that I never hardcode the dynamic local ports.
9. As a developer, I want migrations applied automatically into the discovered Postgres, so that the API is immediately usable after bootstrap.
10. As a developer, I want a health check on `GET /api/v1/health` showing `{"status":"ok","db":"ok","redis":"ok"}`, so that I know the sandbox is genuinely wired to Floci-backed engines.
11. As a developer, I want an optional smoke test that submits a `/api/v1/scrape` job and polls it to `completed`, so that the whole path (API → queue → worker → DB → Redis) is verified end-to-end.
12. As a developer, I want the negotiated downgrades from prod to be recorded with rationale, so that the sandbox's deviations remain known, small, and auditable.
13. As a developer, I want the normal local dev flow (`docker compose up -d`) untouched by the sandbox, so that day-to-day development is unaffected.
14. As a developer, I want a teardown command (`docker compose --profile sandbox down -v`) that removes all sandbox containers and volumes, so that cleanup is clean and complete.
15. As a developer, I want sandbox artifacts (Floci data, local state, tfplan files) gitignored while keeping docs trackable, so that the repo stays clean.
16. As a developer, I want the README to document prerequisites and usage of the sandbox, so that onboarding a new machine is self-service.
17. As a developer, I want to practice AWS CLI patterns against the sandbox (e.g. `aws rds describe-db-instances --endpoint-url http://localhost:4566`), so that the practice covers real tooling, not just Terraform.
18. As a developer, I want the API to be reachable via the ALB if Floci honors ELB v2 wiring, with a fallback to bridge `hostPort: 8080`, so that the app is always reachable in the sandbox.
19. As a developer, I want a warning (not failure) if image push to the local registry fails, so that the environment itself is still usable and the failure mode is understood.
20. As a developer, I want the sandbox to run only when explicitly invoked via the `sandbox` compose profile, so that default local dev and the sandbox do not collide.

## Implementation Decisions

The implementation parameterizes the existing single Terraform module (no duplicate module) and adds a bootstrap script plus a compose service.

### Modules built / modified

1. **`docker-compose.yml`** — add a `floci` service under `profiles: ["sandbox"]`:
   - Image `floci/floci:latest`, port `4566:4566`
   - Docker socket mounted (`${DOCKER_SOCK:-/var/run/docker.sock}`) so Floci can orchestrate the real containers it backs
   - Persistent storage volume `floci_data`, `FLOCI_STORAGE_MODE=persistent`, `FLOCI_DEFAULT_REGION=us-east-1`
   - Healthcheck against `/_localstack/health`
   - The `sandbox` profile guarantees normal `docker compose up -d` is untouched.

2. **`terraform/`** — parameterized in place (default behavior identical; prod preserved):
   - **`main.tf`** — AWS provider conditional on `deploy_mode`; under `floci`: region `us-east-1`, dummy creds (`test`/`test`), skip credential/account-id/metadata validation, and `endpoints { ec2/ecs/ecr/elasticache/rds/iam/logs/sts = "http://localhost:4566" }`.
   - **`variables.tf`** — add `deploy_mode` (default `"aws"`), `floci_endpoint` (default `http://localhost:4566`), fixed sandbox DB creds (`goscrape_admin` / `devpass`).
   - **`ecs.tf`** — task definitions/services use `count`/`dynamic` so Fargate/awsvpc/awslogs/SecretsManager apply under `aws` and the negotiated fallbacks apply under `floci`; ALB block conditionally attached.
   - **`rds.tf` / `elasticache.tf`** — snapshot/encryption/auth attributes conditional on `deploy_mode`.
   - **`outputs.tf`** — add `rds_host`/`rds_port`, `redis_host`/`redis_port`, `api_url` (ALB DNS in aws mode; `http://localhost:8080` in floci mode).
   - **State** — a committed `backend.floci.hcl` (local) swapped in during init for `deploy_mode=floci`.

3. **`scripts/floci-up.sh`** — idempotent, parameterized end-to-end bootstrap:
   - **Preflight** → verify Docker + compose + port availability (abort on failure)
   - **Start** → `docker compose --profile sandbox up -d floci` (abort on failure)
   - **Ready** → poll `GET /_localstack/health` (max 120s, abort on timeout)
   - **Env** → set `AWS_ENDPOINT_URL` / `ACCESS_KEY` / `SECRET` / `REGION`
   - **Apply** → `terraform -chdir=terraform init -backend-config=backend.floci.hcl` + `plan` + `apply -auto-approve -var deploy_mode=floci` (abort on failure)
   - **Push** → build api + worker, tag `localhost:4566/goscrape-*:latest`, `docker push` (warn on failure)
   - **Discover** → RDS/Redis host+port from terraform outputs and `aws rds describe-db-instances` / `aws elasticache describe-replication-groups` with `--endpoint-url http://localhost:4566` (abort if empty)
   - **Migrate** → apply `migrations/000001_create_jobs_table.up.sql` into the discovered RDS via `psql` or `docker exec` heuristic (abort on failure)
   - **Health** → poll `GET http://localhost:8080/api/v1/health` until 200 (report failure)
   - **Smoke (optional)** → `--smoke-test`: POST `/api/v1/scrape`, poll to `completed` (report failure)
   - **Report** → print URLs / creds / endpoints + teardown command

4. **`.gitignore`** — add `.floci-data/`, `terraform/backend.floci.hcl`, `terraform/*.tfplan`; remove the `*.md` glob so docs + spec are trackable.

5. **`README.md`** — new "Floci sandbox (local AWS, $0)" section: prerequisites, run command (`./scripts/floci-up.sh`), what it provisions, teardown, local-only note, link to the negotiated-downgrade table; update the project-structure tree with `scripts/` and `docs/`.

### Fidelity strategy — attempt prod shape first, degrade on rejection

Order of attack per resource group:
1. Keep the prod attribute **as-is**; run `terraform apply` against Floci.
2. If Floci errors or returns unusable values, degrade **that attribute only** by the narrowest sane change.
3. Record each negotiated downgrade so the sandbox's deviation from prod stays *known and small*.

| Resource / attribute (prod) | Attempt (as-is) | Negotiated fallback (only if rejected) | Issue #6 status |
|---|---|---|---|
| VPC / subnets / IGW / SG / route tables | as-is (Floci EC2/ECS backs VPC) | none | **honored as-is** — provisioned under floci |
| `aws_db_instance` (postgres 16, snapshot, encryption) | as-is | `deploy_mode=floci`: drop `final_snapshot` / `storage_encrypted`; `storage_type=gp2`, `backup_retention=0`, no auto-minor-upgrade to match Floci's read-back (idempotency); `password` from `var.db_master_password` instead of `random_password` | **fallback kept** — Floci read-back/idempotency required these |
| `aws_db_parameter_group` | as-is | `lifecycle.ignore_changes=[parameter, tags, tags_all]` (Floci drops on read-back) | **fallback kept** |
| `aws_elasticache_subnet_group` | as-is | omitted under `floci` (Floci: `CreateCacheSubnetGroup not supported`) | **fallback kept** — API rejection observed |
| `aws_elasticache_replication_group` (redis, auth, transit encryption) | as-is | `deploy_mode=floci`: `auth_token=null`, `transit_encryption_enabled=false`, `num_cache_clusters=0` (Floci can't `IncreaseReplicaCount`), endpoint via `floci-valkey-<rgid>` container name (Floci returns no `primary_endpoint_address`); `lifecycle.ignore_changes=[engine, tags, tags_all]` | **fallback kept** — Valkey container + no `primary_endpoint_address` confirmed |
| `aws_ecr_repository` x2 | as-is (real `registry:2`) | none | **honored as-is** — local registry on `:5100` |
| IAM managed-policy attachments (`CloudWatchLogsFullAccess`) | as-is | inline policy under `floci` (managed policy ARN absent in Floci IAM) | **fallback kept** |
| `aws_ecs_cluster` + task definitions | as-is (FARGATE / awsvpc attempt) | `network_mode = "bridge"`, `requires_compatibilities` / `launch_type` omitted under floci (compute layer, issue #3) | **fallback kept** — bridge tasks run; Fargate/awsvpc not usable |
| task logs | `awslogs` → CloudWatch | omit `logConfiguration` under floci (Docker default `json-file`; Floci drops explicit config on read-back) (compute layer, issue #3) | **fallback kept** — explicit `json-file` dropped on read-back |
| config injection | `secrets` from SecretsManager | inline `environment` vars under floci (compute layer, issue #3) | **fallback kept** |
| ALB + target group + listener | as-is (`load_balancer` block) | omitted under `floci` (Floci ELBv2 rejects with `InvalidClientTokenId`); expose API via `hostPort: 8080` (compute layer, issue #3) | **fallback kept** — ALB gated `count=aws` |
| worker auto-scaling / SNS alarms | as-is | omitted under `floci` (Floci: `RegisterScalableTarget not supported`); SNS topic kept | **fallback kept** — SNS retained; autoscaling gated |
| API health after ECS start | `GET http://localhost:8080/api/v1/health` → db+redis ok | Floci attaches `web-gobbler_default` after bridge+`hostPort` start; mitigated by `ConnectTimeout` + Ping retry in `store.New` (issue #3) | **mitigation verified** — health returns ok in sandbox |
| state backend | S3 | S3 **re-pointed at Floci** via `backend.floci.hcl` (not local backend; type cannot change via `-backend-config`); state persists in `floci_data` volume | **fallback kept** — S3-to-Floci, not local state |
| provider configuration | as-is | `deploy_mode=floci`: region `us-east-1`, static `test`/`test` creds, skip validations, `dynamic endpoints` for required services | **fallback kept** |

Pre-existing provider v5 incompatibilities fixed (apply to both modes): `replication_group_description` → `description` (elasticache.tf); `parameters` on `aws_db_instance` → `aws_db_parameter_group` (rds.tf).

Floci read-back quirks (data plane idempotency): SG `ingress`/`egress`, `tags`/`tags_all`, param-group `parameter` blocks are applied to Floci but not returned on describe, so `lifecycle.ignore_changes` suppresses perpetual re-adds. **Terraform cannot make `lifecycle` conditional on `deploy_mode`**, so these ignore rules also apply under `aws` — residual risk when a live AWS stack exists (SG/task-def drift may not be corrected by apply). No attribute that Floci fully honored was left unnecessarily degraded.

#### Issue #6 — prod regression (static; no live AWS)

There is **no live web-gobbler AWS stack** at audit time, so `terraform plan -var deploy_mode=aws` against real state is **deferred** until prod exists.

Static guard completed instead:

- `deploy_mode` defaults to `"aws"`.
- Under `deploy_mode=aws`, Fargate/`awsvpc`/`awslogs`/SecretsManager secrets/ALB/autoscaling/`gp3`/encrypted RDS/ElastiCache auth+transit remain on the aws branches (verified by marker scan of `terraform/*.tf`).
- `terraform validate` succeeds.
- Diff from pre-floci terraform → current is additive `deploy_mode` branching plus the shared provider-v5 fixes above (aws path not stripped of prod attributes).

When live AWS exists: run `terraform plan -var deploy_mode=aws` against that state and confirm no unexpected diffs beyond intentional shared fixes.


### Interface contracts (sandbox)

- `DATABASE_URL=postgresql://goscrape_admin:devpass@localhost:<rds_port>/goscrape`
- `REDIS_URL=redis://localhost:<redis_port>` (token appended if Floci enforces ACL auth)
- Ports are always discovered from outputs — never hardcoded.

## Testing Decisions

There are **no intentional product-feature Go changes** in this PRD. One compute-layer mitigation lives in `store.New` (ConnectTimeout + Ping retry for Floci's hostPort network-attach race; see downgrade table: API health after ECS start). Existing `test/` unit tests are otherwise unaffected. Testing is **infrastructure acceptance**, exercised end-to-end through the bootstrap script and Terraform:

- A good test here verifies **external behavior only**: that a clean machine reaches a running, migrated, health-checked sandbox — not the internal wiring of the script or Terraform internals.
- **Script e2e** — `scripts/floci-up.sh` runs green on a clean machine (Docker Desktop, no AWS creds). Includes the optional `--smoke-test` path (POST a scrape job → poll to `completed`; `GET /api/v1/jobs/:id` returns results).
- **Idempotency** — `terraform apply -var deploy_mode=floci` re-run shows no diff.
- **Health contract** — `GET http://localhost:8080/api/v1/health` returns `{"status":"ok","db":"ok","redis":"ok"}`, proving app ↔ Floci-backed RDS/ElastiCache wiring.
- **Prod regression guard** — static: `deploy_mode` defaults to `aws` and aws-mode branches retain prod attributes (issue #6). Live `terraform plan -var deploy_mode=aws` deferred until a web-gobbler AWS stack exists.
- **Dev-flow guard** — normal `docker compose up -d` is unchanged.
- **Teardown** — `docker compose --profile sandbox down -v` removes all sandbox containers + volumes.
- **Downgrade audit** — the negotiated-downgrade table records every deviation from prod with rationale.

Prior art: the repo currently has only Go unit tests under `test/`; there is no existing infrastructure test harness, so the sandbox acceptance checks above are the first of their kind here.

## Out of Scope

- Public hosting, real AWS account usage, cost recovery.
- CI/CD changes (`.github/workflows/` is empty; skipped).
- Sandbox observability (Prometheus/Grafana out of scope for the sandbox flow).
- Micro-fidelity of AWS networking internals (awsvpc ENI wiring, awslogs driver) — these are the negotiated downgrades.
- Floci CLI installer (the script uses Docker directly).
- External load testing against the sandbox.

## Further Notes

- The real objective is **using AWS infrastructure without paying AWS** — a practice environment, not a public deployment.
- The downgrade table in Implementation Decisions is the single source of truth for how the sandbox deviates from prod; it must be kept in sync as new attributes are attempted.
- Risk highlights from the source spec: Floci attribute rejection (8.1), ECS image pull from sibling registry (8.2, fallback to compose-profile api/worker wired to the same RDS/Redis), ElastiCache ACL auth (8.3), dynamic ports (8.4, always discover), Windows Docker socket shim (8.5), migration container selection (8.6), stale persistent state (8.7, teardown `-v`), and single-module drift between modes (8.8, default `aws` + downgrade table).
- Because `deploy_mode` defaults to `"aws"`, prod applies remain byte-identical; the sandbox is opt-in via the `sandbox` compose profile and the `-var deploy_mode=floci` apply.
# How to deploy Web Gobbler to AWS

This guide walks through provisioning infrastructure on **real** AWS ECS Fargate and deploying the API and worker services.

> [!IMPORTANT]
> There is **no live web-gobbler AWS stack** in the default workflow today. To practice the same Terraform graph locally at **$0**, use the Floci sandbox instead: `./scripts/floci-up.sh` (see [run.md — Ways to run](run.md#ways-to-run) and [FLOCI_SANDBOX.md](../FLOCI_SANDBOX.md)).
>
> Use this guide only when you have real AWS credentials and intend to create billable resources. `deploy_mode` defaults to `"aws"` — do **not** pass `-backend-config=backend.floci.hcl` when targeting real AWS.

## Prerequisites

- An AWS account with IAM permissions for ECS, ECR, RDS, ElastiCache, IAM, and EC2 (VPC)
- Terraform 1.6+ installed — verify with `terraform version`
- Docker installed — verify with `docker version`
- AWS credentials configured — verify with `aws sts get-caller-identity`

> [!WARNING]
> This deployment creates real AWS resources that incur costs. Estimated monthly bill: **$60–100** for the full stack (RDS db.t4g.micro, ElastiCache cache.t4g.micro, NAT Gateway, ALB, ECS Fargate tasks). Remember to run `terraform destroy` when you are done.

## Step 1 — Configure Terraform variables

```bash
cd terraform
cp terraform.tfvars.example terraform.tfvars
```

Edit `terraform.tfvars` with your values:

```hcl
aws_region         = "ap-south-1"
environment        = "prod"
db_instance_class  = "db.t4g.micro"
cache_node_type    = "cache.t4g.micro"
api_min_capacity   = 1
api_max_capacity   = 3
worker_min_capacity = 1
worker_max_capacity = 5
```

All variables are documented in `variables.tf` with descriptions and defaults.

## Step 2 — Initialise and apply

```bash
terraform init
# deploy_mode defaults to "aws" — omit Floci backend overrides
terraform plan
terraform apply
```

Terraform provisions:

| Resource | Description |
|----------|-------------|
| VPC | 2 public + 2 private subnets, Internet Gateway, NAT Gateway |
| RDS PostgreSQL | 20 GB gp3, encrypted, 7-day backup window |
| ElastiCache Redis | 7.1 with transit encryption and auth token |
| ECR repositories | `goscrape-api` and `goscrape-worker` |
| ECS Fargate cluster | With Container Insights enabled |
| ALB | Public-facing, health check on `/api/v1/health` |
| Task definitions | API (linked to ALB) and worker (headless) |
| Auto scaling | Worker scales on `scraper_queue_depth` CloudWatch alarm |
| Secrets Manager | `DATABASE_URL` and `REDIS_URL` stored securely |
| CloudWatch Logs | 30-day retention for both services |

The apply output includes `api_image_url` and `worker_image_url` — you will need these for deployment.

## Step 3 — Build and push Docker images

From the repository root:

```bash
# Authenticate with ECR
aws ecr get-login-password --region ap-south-1 | \
  docker login --username AWS --password-stdin \
  $(terraform output -raw api_image_url | cut -d/ -f1)

# Build and push API image
docker build -f docker/Dockerfile.api -t $(terraform output -raw api_image_url) .
docker push $(terraform output -raw api_image_url)

# Build and push worker image
docker build -f docker/Dockerfile.worker -t $(terraform output -raw worker_image_url) .
docker push $(terraform output -raw worker_image_url)
```

## Step 4 — GitHub Actions (CI only)

This repository’s Actions workflow (`.github/workflows/ci.yml`) runs **vet, build, unit, and integration tests** on pushes and pull requests to `main`. It does **not** deploy to ECS.

Deploy to real AWS with the Terraform + Docker steps above (build/push images to ECR, then force a new ECS deployment or update task definitions). When you add automated CD later, prefer [OIDC to AWS](https://docs.github.com/en/actions/how-tos/secure-your-work/security-harden-deployments/oidc-in-aws) over long-lived access keys.

For a $0 AWS-shaped practice path, use Floci instead: `./scripts/floci-up.sh`.

## Step 5 — Verify the deployment

```bash
curl http://$(terraform output -raw alb_dns)/api/v1/health
```

You should see:

```json
{"status":"ok","db":"ok","redis":"ok","version":"1.0.0"}
```

Submit a test job:

```bash
curl -X POST http://$(terraform output -raw alb_dns)/api/v1/scrape \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com", "extract": ["links"]}'
```

## Troubleshooting

### The health check returns degraded

SSH into a task (or check CloudWatch Logs) and look for connection errors. The two most common causes:

- **RDS security group** — ensure the ECS task security group allows outbound traffic to the RDS security group on port 5432.
- **Secrets Manager** — verify the task execution role has `secretsmanager:GetSecretValue` permission. The Terraform module configures this automatically.

### Tasks keep failing to start

Check the ECS service **Events** tab in the AWS console. Common issues:

- **Insufficient memory** — the task definition may request more memory than the cluster has available. Reduce the task memory or increase the cluster capacity.
- **Image not found** — confirm the image URI in the task definition matches what was pushed to ECR.

### Scaling is not working

The worker auto-scaling policy uses a CloudWatch alarm on the `GoScrape/scraper_queue_depth` metric. If the metric is not being published:

- Check the worker logs for `failed to publish queue depth to cloudwatch` messages.
- Verify the task role has `cloudwatch:PutMetricData` permission.
- The metric publisher runs every 60 seconds — wait at least 2 minutes before expecting a scale event.

## Cleaning up

```bash
terraform destroy
```

This deletes all AWS resources. RDS and ElastiCache have termination protection; you must log in to the AWS console to disable it first.

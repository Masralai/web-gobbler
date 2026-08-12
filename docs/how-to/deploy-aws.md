# How to deploy GoScrape to AWS

This guide walks through provisioning the full infrastructure on AWS ECS Fargate and deploying the API and worker services.

## Prerequisites

- An AWS account with IAM permissions for ECS, ECR, RDS, ElastiCache, IAM, and EC2 (VPC)
- Terraform 1.5+ installed — verify with `terraform version`
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

## Step 4 — Configure GitHub Actions secrets

In your GitHub repository settings, add these secrets:

| Secret | Value |
|--------|-------|
| `AWS_ACCESS_KEY_ID` | IAM access key |
| `AWS_SECRET_ACCESS_KEY` | IAM secret key |
| `ALB_DNS` | ALB DNS name (`terraform output -raw alb_dns`) |

Push to the `main` branch to trigger the CI/CD pipeline:

```
git push origin main
```

The pipeline:

1. Runs `go vet`, unit tests, and integration tests
2. Checks for vulnerabilities with `govulncheck`
3. Builds and pushes both Docker images to ECR
4. Registers new ECS task definitions with the updated images
5. Performs a rolling deployment (API first, then worker)
6. Waits for the health check to pass

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

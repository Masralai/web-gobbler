resource "random_password" "redis" {
  length  = 24
  special = false
}

resource "aws_secretsmanager_secret" "redis_url" {
  name = "goscrape-redis-url-${var.environment_name}"
}

resource "aws_secretsmanager_secret_version" "redis_url" {
  secret_id = aws_secretsmanager_secret.redis_url.id
  secret_string = format(
    "redis://:%s@%s:%d",
    var.deploy_mode == "aws" ? random_password.redis.result : var.redis_auth_token,
    var.deploy_mode == "aws" ? aws_elasticache_replication_group.redis.primary_endpoint_address : "floci-valkey-${aws_elasticache_replication_group.redis.replication_group_id}",
    aws_elasticache_replication_group.redis.port
  )
}

resource "aws_elasticache_subnet_group" "redis" {
  count      = var.deploy_mode == "aws" ? 1 : 0
  name       = "goscrape-${var.environment_name}-redis-subnet"
  subnet_ids = aws_subnet.private[*].id
}

resource "aws_security_group" "redis" {
  name        = "goscrape-${var.environment_name}-redis-sg"
  description = "Allow Redis access from ECS tasks"
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "Redis from ECS tasks"
    from_port       = 6379
    to_port         = 6379
    protocol        = "tcp"
    security_groups = [aws_security_group.ecs_tasks.id]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  lifecycle {
    ignore_changes = [ingress, egress]
  }
}

resource "aws_elasticache_replication_group" "redis" {
  replication_group_id = "goscrape-${var.environment_name}"
  description          = "GoScrape Redis ${var.environment_name}"
  engine_version       = var.redis_engine_version
  node_type            = var.redis_node_type
  num_cache_clusters   = var.deploy_mode == "aws" ? 1 : 0
  port                 = 6379
  parameter_group_name = "default.redis7"

  subnet_group_name  = var.deploy_mode == "aws" ? aws_elasticache_subnet_group.redis[0].name : null
  security_group_ids = [aws_security_group.redis.id]

  auth_token                 = var.deploy_mode == "aws" ? random_password.redis.result : null
  transit_encryption_enabled = var.deploy_mode == "aws"

  automatic_failover_enabled = false
  multi_az_enabled           = false

  lifecycle {
    ignore_changes = [engine, tags, tags_all]
  }

  tags = {
    Name = "goscrape-${var.environment_name}"
  }
}

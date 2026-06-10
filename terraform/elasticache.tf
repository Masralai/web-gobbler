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
    random_password.redis.result,
    aws_elasticache_replication_group.redis.primary_endpoint_address,
    aws_elasticache_replication_group.redis.port
  )
}

resource "aws_elasticache_subnet_group" "redis" {
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
}

resource "aws_elasticache_replication_group" "redis" {
  replication_group_id          = "goscrape-${var.environment_name}"
  replication_group_description = "GoScrape Redis ${var.environment_name}"
  engine                        = "redis"
  engine_version                = var.redis_engine_version
  node_type                     = var.redis_node_type
  num_cache_clusters            = 1
  port                          = 6379
  parameter_group_name          = "default.redis7"

  subnet_group_name          = aws_elasticache_subnet_group.redis.name
  security_group_ids         = [aws_security_group.redis.id]

  auth_token    = random_password.redis.result
  transit_encryption_enabled = true

  automatic_failover_enabled = false
  multi_az_enabled           = false

  tags = {
    Name = "goscrape-${var.environment_name}"
  }
}

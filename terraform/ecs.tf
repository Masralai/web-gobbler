# ──────────────────────────────────────────────
# VPC
# ──────────────────────────────────────────────

resource "aws_vpc" "main" {
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = {
    Name = "goscrape-${var.environment_name}"
  }
}

resource "aws_subnet" "public" {
  count                   = length(var.public_subnet_cidrs)
  vpc_id                  = aws_vpc.main.id
  cidr_block              = var.public_subnet_cidrs[count.index]
  availability_zone       = var.availability_zones[count.index]
  map_public_ip_on_launch = true

  tags = {
    Name = "goscrape-${var.environment_name}-public-${count.index + 1}"
  }
}

resource "aws_subnet" "private" {
  count             = length(var.private_subnet_cidrs)
  vpc_id            = aws_vpc.main.id
  cidr_block        = var.private_subnet_cidrs[count.index]
  availability_zone = var.availability_zones[count.index]

  tags = {
    Name = "goscrape-${var.environment_name}-private-${count.index + 1}"
  }
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id

  tags = {
    Name = "goscrape-${var.environment_name}-igw"
  }
}

resource "aws_eip" "nat" {
  domain = "vpc"

  tags = {
    Name = "goscrape-${var.environment_name}-nat-eip"
  }
}

resource "aws_nat_gateway" "main" {
  allocation_id = aws_eip.nat.id
  subnet_id     = aws_subnet.public[0].id

  tags = {
    Name = "goscrape-${var.environment_name}-nat"
  }
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.main.id
  }

  tags = {
    Name = "goscrape-${var.environment_name}-public-rt"
  }
}

resource "aws_route_table_association" "public" {
  count          = length(aws_subnet.public)
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

resource "aws_route_table" "private" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.main.id
  }

  tags = {
    Name = "goscrape-${var.environment_name}-private-rt"
  }
}

resource "aws_route_table_association" "private" {
  count          = length(aws_subnet.private)
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private.id
}

# ──────────────────────────────────────────────
# ALB
# ──────────────────────────────────────────────

resource "aws_security_group" "alb" {
  count       = var.deploy_mode == "aws" ? 1 : 0
  name        = "goscrape-${var.environment_name}-alb-sg"
  description = "Allow HTTP inbound to ALB"
  vpc_id      = aws_vpc.main.id

  ingress {
    description = "HTTP from internet"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "goscrape-${var.environment_name}-alb-sg"
  }
}

resource "aws_lb" "api" {
  count              = var.deploy_mode == "aws" ? 1 : 0
  name               = "goscrape-${var.environment_name}"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb[0].id]
  subnets            = aws_subnet.public[*].id

  tags = {
    Name = "goscrape-${var.environment_name}"
  }
}

resource "aws_lb_target_group" "api" {
  count       = var.deploy_mode == "aws" ? 1 : 0
  name        = "goscrape-${var.environment_name}-api-tg"
  port        = 8080
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = aws_vpc.main.id

  health_check {
    enabled             = true
    path                = "/health"
    port                = "traffic-port"
    protocol            = "HTTP"
    healthy_threshold   = 2
    unhealthy_threshold = 3
    interval            = 30
    timeout             = 10
    matcher             = "200"
  }

  tags = {
    Name = "goscrape-${var.environment_name}-api-tg"
  }
}

resource "aws_lb_listener" "api" {
  count             = var.deploy_mode == "aws" ? 1 : 0
  load_balancer_arn = aws_lb.api[0].arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.api[0].arn
  }
}

# ──────────────────────────────────────────────
# ECS Task Security Group
# ──────────────────────────────────────────────

resource "aws_security_group" "ecs_tasks" {
  name        = "goscrape-${var.environment_name}-ecs-tasks-sg"
  description = "Allow traffic to ECS tasks"
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "HTTP from ALB"
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = var.deploy_mode == "aws" ? [aws_security_group.alb[0].id] : []
    cidr_blocks     = var.deploy_mode == "aws" ? [] : ["0.0.0.0/0"]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "goscrape-${var.environment_name}-ecs-tasks-sg"
  }
}

# ──────────────────────────────────────────────
# CloudWatch Logs
# ──────────────────────────────────────────────

resource "aws_cloudwatch_log_group" "api" {
  name              = "/ecs/goscrape-api"
  retention_in_days = 30

  tags = {
    Name = "goscrape-${var.environment_name}-api-logs"
  }
}

resource "aws_cloudwatch_log_group" "worker" {
  name              = "/ecs/goscrape-worker"
  retention_in_days = 30

  tags = {
    Name = "goscrape-${var.environment_name}-worker-logs"
  }
}

# ──────────────────────────────────────────────
# IAM
# ──────────────────────────────────────────────

resource "aws_iam_role" "ecs_execution" {
  name = "goscrape-${var.environment_name}-ecs-execution-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Service = "ecs-tasks.amazonaws.com"
      }
      Action = "sts:AssumeRole"
    }]
  })

  lifecycle {
    ignore_changes = [tags, tags_all]
  }
}

resource "aws_iam_role_policy_attachment" "ecs_execution_ecr" {
  role       = aws_iam_role.ecs_execution.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"
}

resource "aws_iam_role_policy_attachment" "ecs_execution_logs" {
  count      = var.deploy_mode == "aws" ? 1 : 0
  role       = aws_iam_role.ecs_execution.name
  policy_arn = "arn:aws:iam::aws:policy/CloudWatchLogsFullAccess"
}

resource "aws_iam_role_policy" "ecs_execution_logs_inline" {
  count = var.deploy_mode == "floci" ? 1 : 0
  name  = "goscrape-${var.environment_name}-ecs-logs-inline"
  role  = aws_iam_role.ecs_execution.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "logs:CreateLogGroup",
        "logs:CreateLogStream",
        "logs:PutLogEvents"
      ]
      Resource = "*"
    }]
  })
}

resource "aws_iam_role_policy" "ecs_execution_secrets" {
  name = "goscrape-${var.environment_name}-ecs-secrets-read"
  role = aws_iam_role.ecs_execution.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "secretsmanager:GetSecretValue"
      ]
      Resource = [
        aws_secretsmanager_secret.db_url.arn,
        aws_secretsmanager_secret.redis_url.arn
      ]
    }]
  })
}

resource "aws_iam_role" "ecs_task" {
  name = "goscrape-${var.environment_name}-ecs-task-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Service = "ecs-tasks.amazonaws.com"
      }
      Action = "sts:AssumeRole"
    }]
  })

  lifecycle {
    ignore_changes = [tags, tags_all]
  }
}

resource "aws_iam_role_policy" "ecs_task_cloudwatch" {
  name = "goscrape-${var.environment_name}-ecs-cloudwatch-metrics"
  role = aws_iam_role.ecs_task.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "cloudwatch:PutMetricData"
      ]
      Resource = "*"
    }]
  })
}

# ──────────────────────────────────────────────
# ECS Cluster
# ──────────────────────────────────────────────

resource "aws_ecs_cluster" "main" {
  name = "goscrape-${var.environment_name}"

  setting {
    name  = "containerInsights"
    value = "enabled"
  }

  tags = {
    Name = "goscrape-${var.environment_name}"
  }
}

# ──────────────────────────────────────────────
# Task Definitions
# ──────────────────────────────────────────────

resource "aws_ecs_task_definition" "api" {
  family                   = "goscrape-api"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = var.api_cpu
  memory                   = var.api_memory
  execution_role_arn       = aws_iam_role.ecs_execution.arn
  task_role_arn            = aws_iam_role.ecs_task.arn

  container_definitions = jsonencode([
    {
      name                   = "api"
      image                  = "${aws_ecr_repository.api.repository_url}:${var.api_image_tag}"
      essential              = true
      readonlyRootFilesystem = true

      portMappings = [
        {
          containerPort = 8080
          hostPort      = 8080
          protocol      = "tcp"
        }
      ]

      secrets = [
        {
          name      = "DATABASE_URL"
          valueFrom = aws_secretsmanager_secret.db_url.arn
        },
        {
          name      = "REDIS_URL"
          valueFrom = aws_secretsmanager_secret.redis_url.arn
        }
      ]

      environment = [
        { name = "PORT", value = "8080" },
        { name = "DEFAULT_TIMEOUT_SEC", value = tostring(var.default_timeout_sec) },
        { name = "DEFAULT_MAX_RETRIES", value = tostring(var.default_max_retries) },
        { name = "LOG_LEVEL", value = var.log_level }
      ]

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.api.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "ecs"
        }
      }

      # ponytail: distroless has no wget/shell; ALB target group health_check covers API
    }
  ])
}

resource "aws_ecs_task_definition" "worker" {
  family                   = "goscrape-worker"
  network_mode             = "awsvpc"
  requires_compatibilities = ["FARGATE"]
  cpu                      = var.worker_cpu
  memory                   = var.worker_memory
  execution_role_arn       = aws_iam_role.ecs_execution.arn
  task_role_arn            = aws_iam_role.ecs_task.arn

  container_definitions = jsonencode([
    {
      name                   = "worker"
      image                  = "${aws_ecr_repository.worker.repository_url}:${var.worker_image_tag}"
      essential              = true
      readonlyRootFilesystem = true

      secrets = [
        {
          name      = "DATABASE_URL"
          valueFrom = aws_secretsmanager_secret.db_url.arn
        },
        {
          name      = "REDIS_URL"
          valueFrom = aws_secretsmanager_secret.redis_url.arn
        }
      ]

      environment = [
        { name = "WORKER_CONCURRENCY", value = tostring(var.worker_concurrency) },
        { name = "DEFAULT_TIMEOUT_SEC", value = tostring(var.default_timeout_sec) },
        { name = "DEFAULT_MAX_RETRIES", value = tostring(var.default_max_retries) },
        { name = "LOG_LEVEL", value = var.log_level }
      ]

      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.worker.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "ecs"
        }
      }
      # ponytail: worker is not an HTTP server; ECS service stability is enough
    }
  ])
}

# ──────────────────────────────────────────────
# ECS Services
# ──────────────────────────────────────────────

resource "aws_ecs_service" "api" {
  name            = "goscrape-api"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.api.arn
  desired_count   = var.api_min_capacity
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = aws_subnet.private[*].id
    security_groups  = [aws_security_group.ecs_tasks.id]
    assign_public_ip = false
  }

  dynamic "load_balancer" {
    for_each = var.deploy_mode == "aws" ? [1] : []
    content {
      target_group_arn = aws_lb_target_group.api[0].arn
      container_name   = "api"
      container_port   = 8080
    }
  }

  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200

  health_check_grace_period_seconds = 30

  depends_on = [
    aws_lb_listener.api[0]
  ]
}

resource "aws_ecs_service" "worker" {
  name            = "goscrape-worker"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.worker.arn
  desired_count   = var.worker_min_capacity
  launch_type     = "FARGATE"

  network_configuration {
    subnets          = aws_subnet.private[*].id
    security_groups  = [aws_security_group.ecs_tasks.id]
    assign_public_ip = false
  }

  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200
}

# ──────────────────────────────────────────────
# Worker Auto Scaling (queue-depth)
# ──────────────────────────────────────────────

resource "aws_appautoscaling_target" "worker" {
  count              = var.deploy_mode == "aws" ? 1 : 0
  max_capacity       = var.worker_max_capacity
  min_capacity       = var.worker_min_capacity
  resource_id        = "service/${aws_ecs_cluster.main.name}/${aws_ecs_service.worker.name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}

resource "aws_cloudwatch_metric_alarm" "worker_queue_high" {
  count               = var.deploy_mode == "aws" ? 1 : 0
  alarm_name          = "goscrape-${var.environment_name}-worker-queue-high"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  metric_name         = "scraper_queue_depth"
  namespace           = "GoScrape"
  period              = 60
  statistic           = "Maximum"
  threshold           = 10
  alarm_description   = "Scale out worker when queue depth exceeds 10"

  dimensions = {
    Environment = var.environment_name
  }
}

resource "aws_cloudwatch_metric_alarm" "worker_queue_low" {
  count               = var.deploy_mode == "aws" ? 1 : 0
  alarm_name          = "goscrape-${var.environment_name}-worker-queue-low"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 5
  metric_name         = "scraper_queue_depth"
  namespace           = "GoScrape"
  period              = 60
  statistic           = "Maximum"
  threshold           = 5
  alarm_description   = "Scale in worker when queue depth is below 5"

  dimensions = {
    Environment = var.environment_name
  }
}

resource "aws_appautoscaling_policy" "worker_scale_out" {
  count              = var.deploy_mode == "aws" ? 1 : 0
  name               = "goscrape-${var.environment_name}-worker-scale-out"
  policy_type        = "StepScaling"
  resource_id        = aws_appautoscaling_target.worker[0].resource_id
  scalable_dimension = aws_appautoscaling_target.worker[0].scalable_dimension
  service_namespace  = aws_appautoscaling_target.worker[0].service_namespace

  step_scaling_policy_configuration {
    adjustment_type         = "ChangeInCapacity"
    cooldown                = 120
    metric_aggregation_type = "Maximum"

    step_adjustment {
      scaling_adjustment          = 1
      metric_interval_lower_bound = 0
    }
  }

  depends_on = [aws_appautoscaling_target.worker]
}

resource "aws_appautoscaling_policy" "worker_scale_in" {
  count              = var.deploy_mode == "aws" ? 1 : 0
  name               = "goscrape-${var.environment_name}-worker-scale-in"
  policy_type        = "StepScaling"
  resource_id        = aws_appautoscaling_target.worker[0].resource_id
  scalable_dimension = aws_appautoscaling_target.worker[0].scalable_dimension
  service_namespace  = aws_appautoscaling_target.worker[0].service_namespace

  step_scaling_policy_configuration {
    adjustment_type         = "ChangeInCapacity"
    cooldown                = 300
    metric_aggregation_type = "Maximum"

    step_adjustment {
      scaling_adjustment          = -1
      metric_interval_upper_bound = 0
    }
  }

  depends_on = [aws_appautoscaling_target.worker]
}

resource "aws_autoscaling_notification" "worker_scale_out" {
  count       = var.deploy_mode == "aws" ? 1 : 0
  group_names = [aws_appautoscaling_target.worker[0].resource_id]
  notifications = [
    "autoscaling:EC2_INSTANCE_LAUNCH",
    "autoscaling:EC2_INSTANCE_TERMINATE",
  ]
  topic_arn = aws_sns_topic.scaling_notifications.arn
}

resource "aws_sns_topic" "scaling_notifications" {
  name = "goscrape-${var.environment_name}-scaling-notifications"
}

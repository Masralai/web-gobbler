resource "random_password" "db" {
  length  = 24
  special = false
}

resource "aws_secretsmanager_secret" "db_url" {
  name = "goscrape-db-url-${var.environment_name}"
}

resource "aws_secretsmanager_secret_version" "db_url" {
  secret_id = aws_secretsmanager_secret.db_url.id
  secret_string = format(
    "postgresql://%s:%s@%s:%d/%s",
    var.db_master_username,
    var.deploy_mode == "aws" ? random_password.db.result : var.db_master_password,
    aws_db_instance.postgres.address,
    aws_db_instance.postgres.port,
    var.db_name
  )
}

resource "aws_db_subnet_group" "postgres" {
  name       = "goscrape-${var.environment_name}-db-subnet"
  subnet_ids = aws_subnet.private[*].id
}

resource "aws_security_group" "rds" {
  name        = "goscrape-${var.environment_name}-rds-sg"
  description = "Allow PostgreSQL access from ECS tasks"
  vpc_id      = aws_vpc.main.id

  ingress {
    description     = "PostgreSQL from ECS tasks"
    from_port       = 5432
    to_port         = 5432
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

resource "aws_db_parameter_group" "postgres" {
  name   = "goscrape-${var.environment_name}-postgres"
  family = "postgres16"

  parameter {
    name  = "log_statement"
    value = "ddl"
  }

  lifecycle {
    ignore_changes = [parameter, tags, tags_all]
  }
}

resource "aws_db_instance" "postgres" {
  identifier        = "goscrape-${var.environment_name}"
  engine            = "postgres"
  engine_version    = var.db_engine_version
  instance_class    = var.db_instance_class
  allocated_storage = var.db_allocated_storage
  storage_type      = var.deploy_mode == "aws" ? "gp3" : "gp2"

  db_name  = var.db_name
  username = var.db_master_username
  password = var.deploy_mode == "aws" ? random_password.db.result : var.db_master_password

  db_subnet_group_name   = aws_db_subnet_group.postgres.name
  vpc_security_group_ids = [aws_security_group.rds.id]
  parameter_group_name   = aws_db_parameter_group.postgres.name

  backup_retention_period = var.deploy_mode == "aws" ? 7 : 0
  backup_window           = var.deploy_mode == "aws" ? "03:00-04:00" : "04:00-06:00"
  maintenance_window      = var.deploy_mode == "aws" ? "sun:04:00-sun:05:00" : "mon:00:00-mon:03:00"

  auto_minor_version_upgrade = var.deploy_mode == "aws"

  skip_final_snapshot       = var.deploy_mode == "floci"
  final_snapshot_identifier = var.deploy_mode == "aws" ? "goscrape-${var.environment_name}-final-${formatdate("YYYY-MM-DD-hhmm", timestamp())}" : null

  storage_encrypted = var.deploy_mode == "aws"

  tags = {
    Name = "goscrape-${var.environment_name}"
  }
}

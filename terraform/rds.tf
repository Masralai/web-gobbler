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
    random_password.db.result,
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
}

resource "aws_db_instance" "postgres" {
  identifier        = "goscrape-${var.environment_name}"
  engine            = "postgres"
  engine_version    = var.db_engine_version
  instance_class    = var.db_instance_class
  allocated_storage = var.db_allocated_storage
  storage_type      = "gp3"

  db_name  = var.db_name
  username = var.db_master_username
  password = random_password.db.result

  db_subnet_group_name   = aws_db_subnet_group.postgres.name
  vpc_security_group_ids = [aws_security_group.rds.id]

  backup_retention_period = 7
  backup_window           = "03:00-04:00"
  maintenance_window      = "sun:04:00-sun:05:00"

  skip_final_snapshot     = false
  final_snapshot_identifier = "goscrape-${var.environment_name}-final-${formatdate("YYYY-MM-DD-hhmm", timestamp())}"

  storage_encrypted = true

  parameters = [
    {
      name  = "log_statement"
      value = "ddl"
    }
  ]

  tags = {
    Name = "goscrape-${var.environment_name}"
  }
}

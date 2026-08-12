variable "deploy_mode" {
  description = "Deployment target: 'aws' (real AWS) or 'floci' (local sandbox at floci_endpoint)"
  type        = string
  default     = "aws"
  validation {
    condition     = contains(["aws", "floci"], var.deploy_mode)
    error_message = "deploy_mode must be either 'aws' or 'floci'."
  }
}

variable "floci_endpoint" {
  description = "Base URL of the Floci (LocalStack-compatible) endpoint for deploy_mode=floci"
  type        = string
  default     = "http://localhost:4566"
}

variable "aws_region" {
  description = "AWS region for all resources"
  type        = string
  default     = "ap-south-1"
}

variable "environment_name" {
  description = "Environment name (e.g. dev, staging, prod)"
  type        = string
  default     = "prod"
}

variable "default_tags" {
  description = "Default tags applied to all resources"
  type        = map(string)
  default = {
    Project     = "goscrape"
    ManagedBy   = "terraform"
    Environment = "prod"
  }
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "availability_zones" {
  description = "List of availability zones"
  type        = list(string)
  default     = ["ap-south-1a", "ap-south-1b"]
}

variable "public_subnet_cidrs" {
  description = "CIDR blocks for public subnets (ALB)"
  type        = list(string)
  default     = ["10.0.1.0/24", "10.0.2.0/24"]
}

variable "private_subnet_cidrs" {
  description = "CIDR blocks for private subnets (ECS tasks)"
  type        = list(string)
  default     = ["10.0.10.0/24", "10.0.11.0/24"]
}

variable "db_instance_class" {
  description = "RDS instance class"
  type        = string
  default     = "db.t4g.micro"
}

variable "db_allocated_storage" {
  description = "RDS allocated storage in GB"
  type        = number
  default     = 20
}

variable "db_engine_version" {
  description = "PostgreSQL engine version"
  type        = string
  default     = "16"
}

variable "db_name" {
  description = "PostgreSQL database name"
  type        = string
  default     = "goscrape"
}

variable "db_master_username" {
  description = "PostgreSQL master username"
  type        = string
  default     = "goscrape_admin"
}

variable "db_master_password" {
  description = "Fixed Postgres master password for deploy_mode=floci (real AWS uses a generated random password)"
  type        = string
  default     = "devpass"
}

variable "redis_auth_token" {
  description = "Fixed Redis auth token for deploy_mode=floci (real AWS uses a generated random token)"
  type        = string
  default     = "devpass"
}

variable "redis_node_type" {
  description = "ElastiCache node type"
  type        = string
  default     = "cache.t4g.micro"
}

variable "redis_engine_version" {
  description = "Redis engine version"
  type        = string
  default     = "7.1"
}

variable "api_cpu" {
  description = "API task CPU units (Fargate)"
  type        = number
  default     = 512
}

variable "api_memory" {
  description = "API task memory in MB (Fargate)"
  type        = number
  default     = 1024
}

variable "api_min_capacity" {
  description = "Minimum number of API tasks"
  type        = number
  default     = 1
}

variable "api_max_capacity" {
  description = "Maximum number of API tasks"
  type        = number
  default     = 3
}

variable "worker_cpu" {
  description = "Worker task CPU units (Fargate)"
  type        = number
  default     = 1024
}

variable "worker_memory" {
  description = "Worker task memory in MB (Fargate)"
  type        = number
  default     = 2048
}

variable "worker_min_capacity" {
  description = "Minimum number of worker tasks"
  type        = number
  default     = 1
}

variable "worker_max_capacity" {
  description = "Maximum number of worker tasks"
  type        = number
  default     = 5
}

variable "worker_concurrency" {
  description = "Worker concurrency per task"
  type        = number
  default     = 5
}

variable "default_timeout_sec" {
  description = "Default HTTP timeout for scrapers"
  type        = number
  default     = 10
}

variable "default_max_retries" {
  description = "Default max retries for scrapers"
  type        = number
  default     = 3
}

variable "log_level" {
  description = "Log level (DEBUG, INFO, WARN, ERROR)"
  type        = string
  default     = "INFO"
}

variable "api_image_tag" {
  description = "Docker image tag for the API"
  type        = string
  default     = "latest"
}

variable "worker_image_tag" {
  description = "Docker image tag for the worker"
  type        = string
  default     = "latest"
}

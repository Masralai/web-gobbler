terraform {
  required_version = ">= 1.6"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }

  backend "s3" {
    bucket = "goscrape-terraform-state"
    key    = "goscrape/terraform.tfstate"
    region = "ap-south-1"
  }
}

provider "aws" {
  region = var.deploy_mode == "aws" ? var.aws_region : "us-east-1"

  access_key = var.deploy_mode == "floci" ? "test" : null
  secret_key = var.deploy_mode == "floci" ? "test" : null

  skip_credentials_validation = var.deploy_mode == "floci"
  skip_requesting_account_id  = var.deploy_mode == "floci"
  skip_metadata_api_check     = var.deploy_mode == "floci"

  default_tags {
    tags = var.default_tags
  }

  dynamic "endpoints" {
    for_each = var.deploy_mode == "floci" ? [1] : []
    content {
      ec2                    = var.floci_endpoint
      ecs                    = var.floci_endpoint
      ecr                    = var.floci_endpoint
      elasticache            = var.floci_endpoint
      rds                    = var.floci_endpoint
      iam                    = var.floci_endpoint
      logs                   = var.floci_endpoint
      sts                    = var.floci_endpoint
      secretsmanager         = var.floci_endpoint
      sns                    = var.floci_endpoint
      cloudwatch             = var.floci_endpoint
      elasticloadbalancing   = var.floci_endpoint
      autoscaling            = var.floci_endpoint
      applicationautoscaling = var.floci_endpoint
    }
  }
}

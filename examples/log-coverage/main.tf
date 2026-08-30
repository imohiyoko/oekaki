# A small estate, written to make a log coverage map say something.
#
# Everything here is invented. The point is not the infrastructure — it is that
# an overlay can be laid over it and produce every state the map can reach,
# including the two that are about the map's own limits rather than about the
# infrastructure.
#
# Read, never applied. examples/log-coverage/plan.json is the `terraform show
# -json` output this produces.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5"
    }
  }
}

provider "aws" {
  region = "eu-west-1"
}

resource "aws_vpc" "main" {
  cidr_block           = "10.20.0.0/16"
  enable_dns_hostnames = true
}

resource "aws_subnet" "private_a" {
  vpc_id            = aws_vpc.main.id
  cidr_block        = "10.20.1.0/24"
  availability_zone = "eu-west-1a"
}

resource "aws_subnet" "private_b" {
  vpc_id            = aws_vpc.main.id
  cidr_block        = "10.20.2.0/24"
  availability_zone = "eu-west-1b"
}

# Two log groups, and the overlay will find that only one of them has anything
# arriving in it.
resource "aws_cloudwatch_log_group" "app" {
  name              = "/platform/app"
  retention_in_days = 30
}

resource "aws_cloudwatch_log_group" "batch" {
  name              = "/platform/batch"
  retention_in_days = 7
}

resource "aws_lb" "public" {
  name               = "public"
  internal           = false
  load_balancer_type = "application"
  subnets            = [aws_subnet.private_a.id, aws_subnet.private_b.id]
}

resource "aws_ecs_cluster" "main" {
  name = "main"
}

resource "aws_ecs_service" "api" {
  name          = "api"
  cluster       = aws_ecs_cluster.main.id
  desired_count = 3
  launch_type   = "FARGATE"
}

resource "aws_ecs_service" "checkout" {
  name          = "checkout"
  cluster       = aws_ecs_cluster.main.id
  desired_count = 2
  launch_type   = "FARGATE"
}

resource "aws_ecs_service" "search" {
  name          = "search"
  cluster       = aws_ecs_cluster.main.id
  desired_count = 1
  launch_type   = "FARGATE"
}

resource "aws_db_instance" "main" {
  identifier           = "main"
  allocated_storage    = 50
  engine               = "postgres"
  engine_version       = "16.3"
  instance_class       = "db.t4g.small"
  multi_az             = false
  db_subnet_group_name = aws_db_subnet_group.main.name
}

resource "aws_db_subnet_group" "main" {
  name       = "main"
  subnet_ids = [aws_subnet.private_a.id, aws_subnet.private_b.id]
}

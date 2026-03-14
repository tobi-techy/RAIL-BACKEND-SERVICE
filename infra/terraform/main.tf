terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
  # Store state in S3 — create the bucket manually once before first apply
  backend "s3" {
    bucket = "rail-terraform-state-605894285151"
    key    = "staging/terraform.tfstate"
    region = "us-east-1"
  }
}

provider "aws" {
  region = var.aws_region
}

# ── VPC ──────────────────────────────────────────────────────────────────────

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.0"

  name = "rail-${var.env}"
  cidr = "10.0.0.0/16"

  azs             = ["${var.aws_region}a", "${var.aws_region}b"]
  public_subnets  = ["10.0.1.0/24", "10.0.2.0/24"]
  private_subnets = ["10.0.11.0/24", "10.0.12.0/24"]

  enable_nat_gateway   = false
  single_nat_gateway   = false
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = local.tags
}

# ── Security Groups ───────────────────────────────────────────────────────────

resource "aws_security_group" "alb" {
  name   = "rail-${var.env}-alb"
  vpc_id = module.vpc.vpc_id

  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = local.tags
}

resource "aws_security_group" "app" {
  name   = "rail-${var.env}-app"
  vpc_id = module.vpc.vpc_id

  ingress {
    from_port       = 8080
    to_port         = 8080
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
  tags = local.tags
}

resource "aws_security_group" "rds" {
  name   = "rail-${var.env}-rds"
  vpc_id = module.vpc.vpc_id

  ingress {
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [aws_security_group.app.id]
  }
  tags = local.tags
}

resource "aws_security_group" "redis" {
  name   = "rail-${var.env}-redis"
  vpc_id = module.vpc.vpc_id

  ingress {
    from_port       = 6379
    to_port         = 6379
    protocol        = "tcp"
    security_groups = [aws_security_group.app.id]
  }
  tags = local.tags
}

# ── RDS (Postgres) ────────────────────────────────────────────────────────────
# db.t3.micro = free tier eligible (750h/month for 12 months)

resource "aws_db_subnet_group" "main" {
  name       = "rail-${var.env}"
  subnet_ids = module.vpc.private_subnets
  tags       = local.tags
}

resource "aws_db_instance" "postgres" {
  identifier        = "rail-${var.env}"
  engine            = "postgres"
  engine_version    = "15"
  instance_class    = "db.t3.micro"  # free tier
  allocated_storage = 20             # free tier max
  storage_type      = "gp2"

  db_name  = "rail"
  username = "rail"
  password = var.db_password

  db_subnet_group_name   = aws_db_subnet_group.main.name
  vpc_security_group_ids = [aws_security_group.rds.id]

  backup_retention_period = 0  # free tier restriction: max 0
  skip_final_snapshot     = false
  final_snapshot_identifier = "rail-${var.env}-final"
  deletion_protection     = true

  # Cost: no multi-AZ, no read replica for free tier
  multi_az = false

  tags = local.tags
}

# ── ElastiCache (Redis) ───────────────────────────────────────────────────────
# cache.t3.micro = free tier eligible (750h/month for 12 months)

resource "aws_elasticache_subnet_group" "main" {
  name       = "rail-${var.env}"
  subnet_ids = module.vpc.private_subnets
}

resource "aws_elasticache_cluster" "redis" {
  cluster_id           = "rail-${var.env}"
  engine               = "redis"
  node_type            = "cache.t3.micro"  # free tier
  num_cache_nodes      = 1
  parameter_group_name = "default.redis7"
  engine_version       = "7.0"
  port                 = 6379
  subnet_group_name    = aws_elasticache_subnet_group.main.name
  security_group_ids   = [aws_security_group.redis.id]

  tags = local.tags
}

# ── ECR ───────────────────────────────────────────────────────────────────────

resource "aws_ecr_repository" "app" {
  name                 = "rail-backend"
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = local.tags
}

# Keep only last 5 images to stay within free tier (500MB)
resource "aws_ecr_lifecycle_policy" "app" {
  repository = aws_ecr_repository.app.name
  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep last 5 images"
      selection = {
        tagStatus   = "any"
        countType   = "imageCountMoreThan"
        countNumber = 5
      }
      action = { type = "expire" }
    }]
  })
}

# ── ECS Cluster ───────────────────────────────────────────────────────────────

resource "aws_ecs_cluster" "main" {
  name = "rail-${var.env}"

  setting {
    name  = "containerInsights"
    value = "disabled"
  }

  tags = local.tags
}

# ── EC2 Instance (t2.micro — free tier) ──────────────────────────────────────

data "aws_ami" "ecs_optimized" {
  most_recent = true
  owners      = ["amazon"]
  filter {
    name   = "name"
    values = ["al2023-ami-ecs-hvm-*-x86_64"]
  }
}

resource "aws_iam_role" "ec2_instance" {
  name = "rail-${var.env}-ec2-instance"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })
}

resource "aws_iam_role_policy_attachment" "ec2_ecs" {
  role       = aws_iam_role.ec2_instance.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonEC2ContainerServiceforEC2Role"
}

resource "aws_iam_instance_profile" "ec2" {
  name = "rail-${var.env}-ec2"
  role = aws_iam_role.ec2_instance.name
}

resource "aws_instance" "app" {
  ami                         = data.aws_ami.ecs_optimized.id
  instance_type               = "t3.micro"
  subnet_id                   = module.vpc.public_subnets[0]
  vpc_security_group_ids      = [aws_security_group.app.id]
  iam_instance_profile        = aws_iam_instance_profile.ec2.name
  associate_public_ip_address = true

  user_data = base64encode("#!/bin/bash\necho ECS_CLUSTER=rail-${var.env} >> /etc/ecs/ecs.config\n")

  tags = merge(local.tags, { Name = "rail-${var.env}" })
}

# ── IAM ───────────────────────────────────────────────────────────────────────

resource "aws_iam_role" "ecs_task_execution" {
  name = "rail-${var.env}-ecs-execution"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
    }]
  })
  tags = local.tags
}

resource "aws_iam_role_policy_attachment" "ecs_task_execution" {
  role       = aws_iam_role.ecs_task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# Allow ECS to read SSM parameters for secrets
resource "aws_iam_role_policy" "ecs_ssm" {
  name = "ssm-read"
  role = aws_iam_role.ecs_task_execution.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["ssm:GetParameters", "ssm:GetParameter"]
      Resource = "arn:aws:ssm:${var.aws_region}:*:parameter/rail/${var.env}/*"
    }]
  })
}

resource "aws_iam_role" "ecs_task" {
  name = "rail-${var.env}-ecs-task"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
    }]
  })
  tags = local.tags
}

# ── CloudWatch Log Group ──────────────────────────────────────────────────────

resource "aws_cloudwatch_log_group" "app" {
  name              = "/ecs/rail-${var.env}"
  retention_in_days = 7  # cost: short retention keeps CloudWatch costs low
  tags              = local.tags
}

# ── ECS Task Definition ───────────────────────────────────────────────────────

resource "aws_ecs_task_definition" "app" {
  family                   = "rail-${var.env}"
  network_mode             = "host"
  requires_compatibilities = ["EC2"]
  execution_role_arn       = aws_iam_role.ecs_task_execution.arn
  task_role_arn            = aws_iam_role.ecs_task.arn

  container_definitions = jsonencode([{
    name      = "rail-backend"
    image     = "${aws_ecr_repository.app.repository_url}:latest"
    essential = true
    memory    = 450

    portMappings = [{
      containerPort = 8080
      protocol      = "tcp"
    }]

    environment = [
      { name = "ENVIRONMENT",    value = var.env },
      { name = "GIN_MODE",       value = "release" },
      { name = "PORT",           value = "8080" },
      { name = "REDIS_HOST",     value = aws_elasticache_cluster.redis.cache_nodes[0].address },
      { name = "REDIS_PORT",     value = "6379" },
      { name = "OTEL_SDK_DISABLED", value = "true" },
      { name = "EMAIL_PROVIDER",   value = "unosend" },
    ]

    # Secrets pulled from SSM Parameter Store at container start
    secrets = [
      { name = "UNOSEND_API_KEY",       valueFrom = "/rail/${var.env}/UNOSEND_API_KEY" },
      { name = "EMAIL_FROM_EMAIL",      valueFrom = "/rail/${var.env}/EMAIL_FROM_EMAIL" },
      { name = "EMAIL_FROM_NAME",       valueFrom = "/rail/${var.env}/EMAIL_FROM_NAME" },
      { name = "JWT_SECRET",            valueFrom = "/rail/${var.env}/JWT_SECRET" },
      { name = "ENCRYPTION_KEY",        valueFrom = "/rail/${var.env}/ENCRYPTION_KEY" },
      { name = "DATABASE_URL",          valueFrom = "/rail/${var.env}/DATABASE_URL" },
      { name = "DATABASE_HOST",         valueFrom = "/rail/${var.env}/DATABASE_HOST" },
      { name = "DATABASE_USER",         valueFrom = "/rail/${var.env}/DATABASE_USER" },
      { name = "DATABASE_PASSWORD",     valueFrom = "/rail/${var.env}/DATABASE_PASSWORD" },
      { name = "DATABASE_NAME",         valueFrom = "/rail/${var.env}/DATABASE_NAME" },
      { name = "DATABASE_SSL_MODE",     valueFrom = "/rail/${var.env}/DATABASE_SSL_MODE" },
      { name = "ALPACA_API_KEY",        valueFrom = "/rail/${var.env}/ALPACA_API_KEY" },
      { name = "ALPACA_API_SECRET",     valueFrom = "/rail/${var.env}/ALPACA_API_SECRET" },
      { name = "ALPACA_BASE_URL",       valueFrom = "/rail/${var.env}/ALPACA_BASE_URL" },
      { name = "ALPACA_DATA_BASE_URL",  valueFrom = "/rail/${var.env}/ALPACA_DATA_BASE_URL" },
      { name = "ALPACA_ENVIRONMENT",    valueFrom = "/rail/${var.env}/ALPACA_ENVIRONMENT" },
      { name = "ALPACA_WEBHOOK_SECRET", valueFrom = "/rail/${var.env}/ALPACA_WEBHOOK_SECRET" },
      { name = "ALPACA_FIRM_ACCOUNT_NO",valueFrom = "/rail/${var.env}/ALPACA_FIRM_ACCOUNT_NO" },
      { name = "BRIDGE_API_KEY",        valueFrom = "/rail/${var.env}/BRIDGE_API_KEY" },
      { name = "BRIDGE_BASE_URL",       valueFrom = "/rail/${var.env}/BRIDGE_BASE_URL" },
      { name = "BRIDGE_WEBHOOK_SECRET", valueFrom = "/rail/${var.env}/BRIDGE_WEBHOOK_SECRET" },
      { name = "OPENAI_API_KEY",        valueFrom = "/rail/${var.env}/OPENAI_API_KEY" },
      { name = "GEMINI_API_KEY",        valueFrom = "/rail/${var.env}/GEMINI_API_KEY" },
      { name = "NEWS_API_KEY",          valueFrom = "/rail/${var.env}/NEWS_API_KEY" },
    ]

    logConfiguration = {
      logDriver = "awslogs"
      options = {
        "awslogs-group"         = aws_cloudwatch_log_group.app.name
        "awslogs-region"        = var.aws_region
        "awslogs-stream-prefix" = "ecs"
      }
    }
  }])

  tags = local.tags
}

# ── ALB ───────────────────────────────────────────────────────────────────────

resource "aws_lb" "main" {
  name               = "rail-${var.env}"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = module.vpc.public_subnets

  tags = local.tags
}

resource "aws_lb_target_group" "app" {
  name        = "rail-${var.env}-ec2"
  port        = 8080
  protocol    = "HTTP"
  vpc_id      = module.vpc.vpc_id
  target_type = "instance"

  health_check {
    path                = "/health"
    healthy_threshold   = 2
    unhealthy_threshold = 3
    interval            = 30
  }

  tags = local.tags
}

resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.main.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type = "redirect"
    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.main.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = "arn:aws:acm:us-east-1:605894285151:certificate/4c0b3d80-ffc8-4846-a45e-0922bca0d192"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.app.arn
  }
}

# ── ECS Service ───────────────────────────────────────────────────────────────

resource "aws_ecs_service" "app" {
  name            = "rail-${var.env}"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.app.arn
  desired_count   = 1
  launch_type     = "EC2"

  load_balancer {
    target_group_arn = aws_lb_target_group.app.arn
    container_name   = "rail-backend"
    container_port   = 8080
  }

  health_check_grace_period_seconds  = 120
  deployment_minimum_healthy_percent = 0
  deployment_maximum_percent         = 100

  lifecycle {
    ignore_changes = [desired_count]
  }

  depends_on = [aws_lb_listener.http, aws_instance.app]
  tags       = local.tags
}

# ── Locals ────────────────────────────────────────────────────────────────────

locals {
  tags = {
    Project     = "rail"
    Environment = var.env
    ManagedBy   = "terraform"
  }
}

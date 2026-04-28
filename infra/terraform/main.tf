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
    bucket         = "rail-terraform-state-885160773772"
    key            = "terraform.tfstate"
    region         = "us-east-1"
    encrypt        = true
    dynamodb_table = "rail-terraform-lock"
  }
}

provider "aws" {
  region = var.aws_region
}

# ── VPC ──────────────────────────────────────────────────────────────────────

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.0"

  name = "rail-${local.env}"
  cidr = "10.0.0.0/16"

  azs             = ["${var.aws_region}a", "${var.aws_region}b"]
  public_subnets  = ["10.0.1.0/24", "10.0.2.0/24"]
  private_subnets = ["10.0.11.0/24", "10.0.12.0/24"]

  enable_nat_gateway   = true
  single_nat_gateway   = true
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = local.tags
}

# ── Security Groups ───────────────────────────────────────────────────────────

resource "aws_security_group" "alb" {
  name   = "rail-${local.env}-alb"
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
  name   = "rail-${local.env}-app"
  vpc_id = module.vpc.vpc_id

  ingress {
    from_port       = 32768
    to_port         = 65535
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
  name   = "rail-${local.env}-rds"
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
  name   = "rail-${local.env}-redis"
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
  name       = "rail-${local.env}"
  subnet_ids = module.vpc.private_subnets
  tags       = local.tags
}

resource "aws_db_instance" "postgres" {
  identifier        = "rail-${local.env}"
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

  backup_retention_period = 7
  skip_final_snapshot     = false
  final_snapshot_identifier = "rail-${local.env}-final"
  deletion_protection     = true
  storage_encrypted       = true

  # Cost: no multi-AZ, no read replica for free tier
  multi_az = false

  tags = local.tags
}

# ── ElastiCache (Redis) ───────────────────────────────────────────────────────
# cache.t3.micro = free tier eligible (750h/month for 12 months)

resource "aws_elasticache_subnet_group" "main" {
  name       = "rail-${local.env}"
  subnet_ids = module.vpc.private_subnets
}

resource "aws_elasticache_replication_group" "redis" {
  replication_group_id = "rail-${local.env}"
  description          = "Rail ${local.env} Redis"
  engine               = "redis"
  node_type            = "cache.t3.micro"
  num_cache_clusters   = 1
  parameter_group_name = "default.redis7"
  engine_version       = "7.0"
  port                 = 6379
  subnet_group_name    = aws_elasticache_subnet_group.main.name
  security_group_ids   = [aws_security_group.redis.id]

  transit_encryption_enabled = true
  at_rest_encryption_enabled = true

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
  name = "rail-${local.env}"

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
  name = "rail-${local.env}-ec2-instance"
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
  name = "rail-${local.env}-ec2"
  role = aws_iam_role.ec2_instance.name
}

resource "aws_instance" "app" {
  ami                         = data.aws_ami.ecs_optimized.id
  instance_type               = "t3.micro"
  subnet_id                   = module.vpc.private_subnets[0]
  vpc_security_group_ids      = [aws_security_group.app.id]
  iam_instance_profile        = aws_iam_instance_profile.ec2.name
  associate_public_ip_address = false

  user_data = base64encode("#!/bin/bash\necho ECS_CLUSTER=rail-${local.env} >> /etc/ecs/ecs.config\n")

  tags = merge(local.tags, { Name = "rail-${local.env}" })
}

# ── IAM ───────────────────────────────────────────────────────────────────────

resource "aws_iam_role" "ecs_task_execution" {
  name = "rail-${local.env}-ecs-execution"
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
      Resource = "arn:aws:ssm:${var.aws_region}:*:parameter/rail/${local.env}/*"
    }]
  })
}

resource "aws_iam_role" "ecs_task" {
  name = "rail-${local.env}-ecs-task"
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
  name              = "/ecs/rail-${local.env}"
  retention_in_days = 7  # cost: short retention keeps CloudWatch costs low
  tags              = local.tags
}

# ── ECS Task Definition ───────────────────────────────────────────────────────

resource "aws_ecs_task_definition" "app" {
  family                   = "rail-${local.env}"
  network_mode             = "bridge"
  requires_compatibilities = ["EC2"]
  execution_role_arn       = aws_iam_role.ecs_task_execution.arn
  task_role_arn            = aws_iam_role.ecs_task.arn

  container_definitions = jsonencode([{
    name      = "rail-backend"
    image     = "${aws_ecr_repository.app.repository_url}:${local.env}-latest"
    essential = true
    memory    = 450

    portMappings = [{
      containerPort = 8080
      hostPort      = 0
      protocol      = "tcp"
    }]

    environment = [
      { name = "ENVIRONMENT",    value = local.env },
      { name = "GIN_MODE",       value = "release" },
      { name = "PORT",           value = "8080" },
      { name = "REDIS_HOST",     value = aws_elasticache_replication_group.redis.primary_endpoint_address },
      { name = "REDIS_PORT",     value = "6379" },
      { name = "REDIS_TLS",      value = "true" },
      { name = "REDIS_PASSWORD", value = "" },
      { name = "OTEL_SDK_DISABLED", value = "true" },
    ]

    # Secrets pulled from SSM Parameter Store at container start
    secrets = [
      { name = "EMAIL_PROVIDER",        valueFrom = "/rail/${local.env}/EMAIL_PROVIDER" },
      { name = "RESEND_API_KEY",        valueFrom = "/rail/${local.env}/RESEND_API_KEY" },
      { name = "EMAIL_FROM_EMAIL",      valueFrom = "/rail/${local.env}/EMAIL_FROM_EMAIL" },
      { name = "EMAIL_FROM_NAME",       valueFrom = "/rail/${local.env}/EMAIL_FROM_NAME" },
      { name = "EMAIL_REPLY_TO",        valueFrom = "/rail/${local.env}/EMAIL_REPLY_TO" },
      { name = "DIDIT_API_KEY",         valueFrom = "/rail/${local.env}/DIDIT_API_KEY" },
      { name = "DIDIT_WORKFLOW_ID",     valueFrom = "/rail/${local.env}/DIDIT_WORKFLOW_ID" },
      { name = "DIDIT_WEBHOOK_SECRET",  valueFrom = "/rail/${local.env}/DIDIT_WEBHOOK_SECRET" },
      { name = "JWT_SECRET",            valueFrom = "/rail/${local.env}/JWT_SECRET" },
      { name = "ENCRYPTION_KEY",        valueFrom = "/rail/${local.env}/ENCRYPTION_KEY" },
      { name = "DATABASE_URL",          valueFrom = "/rail/${local.env}/DATABASE_URL" },
      { name = "DATABASE_HOST",         valueFrom = "/rail/${local.env}/DATABASE_HOST" },
      { name = "DATABASE_USER",         valueFrom = "/rail/${local.env}/DATABASE_USER" },
      { name = "DATABASE_PASSWORD",     valueFrom = "/rail/${local.env}/DATABASE_PASSWORD" },
      { name = "DATABASE_NAME",         valueFrom = "/rail/${local.env}/DATABASE_NAME" },
      { name = "DATABASE_SSL_MODE",     valueFrom = "/rail/${local.env}/DATABASE_SSL_MODE" },
      { name = "ALPACA_API_KEY",        valueFrom = "/rail/${local.env}/ALPACA_API_KEY" },
      { name = "ALPACA_API_SECRET",     valueFrom = "/rail/${local.env}/ALPACA_API_SECRET" },
      { name = "ALPACA_BASE_URL",       valueFrom = "/rail/${local.env}/ALPACA_BASE_URL" },
      { name = "ALPACA_DATA_BASE_URL",  valueFrom = "/rail/${local.env}/ALPACA_DATA_BASE_URL" },
      { name = "ALPACA_ENVIRONMENT",    valueFrom = "/rail/${local.env}/ALPACA_ENVIRONMENT" },
      { name = "ALPACA_WEBHOOK_SECRET", valueFrom = "/rail/${local.env}/ALPACA_WEBHOOK_SECRET" },
      { name = "ALPACA_FIRM_ACCOUNT_NO",valueFrom = "/rail/${local.env}/ALPACA_FIRM_ACCOUNT_NO" },
      { name = "BRIDGE_API_KEY",        valueFrom = "/rail/${local.env}/BRIDGE_API_KEY" },
      { name = "BRIDGE_BASE_URL",       valueFrom = "/rail/${local.env}/BRIDGE_BASE_URL" },
      { name = "BRIDGE_ENVIRONMENT",    valueFrom = "/rail/${local.env}/BRIDGE_ENVIRONMENT" },
      { name = "BRIDGE_WEBHOOK_SECRET", valueFrom = "/rail/${local.env}/BRIDGE_WEBHOOK_SECRET" },
      { name = "LULO_API_KEY",          valueFrom = "/rail/${local.env}/LULO_API_KEY" },
      { name = "LULO_SOLANA_RPC",       valueFrom = "/rail/${local.env}/LULO_SOLANA_RPC" },
      { name = "LULO_OWNER_WALLET",     valueFrom = "/rail/${local.env}/LULO_OWNER_WALLET" },
      { name = "LULO_PRIVATE_KEY",      valueFrom = "/rail/${local.env}/LULO_PRIVATE_KEY" },
      { name = "LULO_BRIDGE_SOURCE_WALLET_ID", valueFrom = "/rail/${local.env}/LULO_BRIDGE_SOURCE_WALLET_ID" },
      { name = "APPLE_TEAM_ID",         valueFrom = "/rail/${local.env}/APPLE_TEAM_ID" },
      { name = "APPLE_KEY_ID",          valueFrom = "/rail/${local.env}/APPLE_KEY_ID" },
      { name = "APPLE_PRIVATE_KEY",     valueFrom = "/rail/${local.env}/APPLE_PRIVATE_KEY" },
      { name = "OPENAI_API_KEY",        valueFrom = "/rail/${local.env}/OPENAI_API_KEY" },
      { name = "GEMINI_API_KEY",        valueFrom = "/rail/${local.env}/GEMINI_API_KEY" },
      { name = "NEWS_API_KEY",          valueFrom = "/rail/${local.env}/NEWS_API_KEY" },
      { name = "PAJ_API_KEY",           valueFrom = "/rail/${local.env}/PAJ_API_KEY" },
      { name = "PAJ_BASE_URL",          valueFrom = "/rail/${local.env}/PAJ_BASE_URL" },
      { name = "PAJ_CHAIN",             valueFrom = "/rail/${local.env}/PAJ_CHAIN" },
      { name = "PAJ_WALLET_ADDRESS",    valueFrom = "/rail/${local.env}/PAJ_WALLET_ADDRESS" },
      { name = "PAJ_TOKEN_MINT",        valueFrom = "/rail/${local.env}/PAJ_TOKEN_MINT" },
      # SNS Push Notifications
      { name = "SNS_PUSH_REGION",              valueFrom = "/rail/${local.env}/SNS_PUSH_REGION" },
      { name = "SNS_PUSH_IOS_PLATFORM_ARN",    valueFrom = "/rail/${local.env}/SNS_PUSH_IOS_PLATFORM_ARN" },
      { name = "SNS_PUSH_ANDROID_PLATFORM_ARN", valueFrom = "/rail/${local.env}/SNS_PUSH_ANDROID_PLATFORM_ARN" },
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
  name               = "rail-${local.env}"
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.alb.id]
  subnets            = module.vpc.public_subnets

  tags = local.tags
}

resource "aws_lb_target_group" "app" {
  name                 = "rail-${local.env}-ec2"
  port                 = 8080
  protocol             = "HTTP"
  vpc_id               = module.vpc.vpc_id
  target_type          = "instance"
  deregistration_delay = 30

  health_check {
    path                = "/health"
    healthy_threshold   = 2
    unhealthy_threshold = 3
    interval            = 30
  }

  tags = local.tags
}

resource "aws_acm_certificate" "api" {
  domain_name       = local.domain
  validation_method = "DNS"
  tags              = local.tags

  lifecycle {
    create_before_destroy = true
  }
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
  certificate_arn   = aws_acm_certificate.api.arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.app.arn
  }
}

# ── ECS Service ───────────────────────────────────────────────────────────────

resource "aws_ecs_service" "app" {
  name                   = "rail-${local.env}"
  cluster                = aws_ecs_cluster.main.id
  task_definition        = aws_ecs_task_definition.app.arn
  desired_count          = 1
  launch_type            = "EC2"
  enable_execute_command = true

  load_balancer {
    target_group_arn = aws_lb_target_group.app.arn
    container_name   = "rail-backend"
    container_port   = 8080
  }

  health_check_grace_period_seconds  = 120
  deployment_minimum_healthy_percent = 100
  deployment_maximum_percent         = 200

  lifecycle {
    ignore_changes = [desired_count]
  }

  depends_on = [aws_lb_listener.http, aws_instance.app]
  tags       = local.tags
}

# ── DynamoDB Terraform Lock ────────────────────────────────────────────────────

resource "aws_dynamodb_table" "terraform_lock" {
  name         = "rail-terraform-lock"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "LockID"
  attribute {
    name = "LockID"
    type = "S"
  }
  tags = local.tags
}

# ── WAF ───────────────────────────────────────────────────────────────────────

resource "aws_wafv2_web_acl" "main" {
  name  = "${var.project_name}-waf"
  scope = "REGIONAL"
  default_action {
    allow {}
  }
  rule {
    name     = "aws-managed-common"
    priority = 1
    override_action {
      none {}
    }
    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesCommonRuleSet"
        vendor_name = "AWS"
      }
    }
    visibility_config {
      sampled_requests_enabled   = true
      cloudwatch_metrics_enabled = true
      metric_name                = "awsCommonRules"
    }
  }
  rule {
    name     = "rate-limit"
    priority = 2
    action {
      block {}
    }
    statement {
      rate_based_statement {
        limit              = 2000
        aggregate_key_type = "IP"
      }
    }
    visibility_config {
      sampled_requests_enabled   = true
      cloudwatch_metrics_enabled = true
      metric_name                = "rateLimitRule"
    }
  }
  visibility_config {
    sampled_requests_enabled   = true
    cloudwatch_metrics_enabled = true
    metric_name                = "railWaf"
  }
  tags = local.tags
}

resource "aws_wafv2_web_acl_association" "alb" {
  resource_arn = aws_lb.main.arn
  web_acl_arn  = aws_wafv2_web_acl.main.arn
}

# ── VPC Flow Logs ─────────────────────────────────────────────────────────────

resource "aws_cloudwatch_log_group" "flow_log" {
  name              = "/vpc/${var.project_name}-flow-logs"
  retention_in_days = 30
  tags              = local.tags
}

resource "aws_flow_log" "vpc" {
  vpc_id                   = module.vpc.vpc_id
  traffic_type             = "ALL"
  log_destination_type     = "cloud-watch-logs"
  log_destination          = aws_cloudwatch_log_group.flow_log.arn
  iam_role_arn             = aws_iam_role.flow_log.arn
  max_aggregation_interval = 60
  tags                     = local.tags
}

resource "aws_iam_role" "flow_log" {
  name = "${var.project_name}-flow-log-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "vpc-flow-logs.amazonaws.com" }
    }]
  })
  tags = local.tags
}

resource "aws_iam_role_policy" "flow_log" {
  name = "flow-log-policy"
  role = aws_iam_role.flow_log.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action   = ["logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents", "logs:DescribeLogGroups", "logs:DescribeLogStreams"]
      Effect   = "Allow"
      Resource = "*"
    }]
  })
}

# ── CloudTrail ────────────────────────────────────────────────────────────────

resource "aws_cloudtrail" "main" {
  name                          = "${var.project_name}-trail"
  s3_bucket_name                = aws_s3_bucket.cloudtrail.id
  include_global_service_events = true
  is_multi_region_trail         = false
  enable_log_file_validation    = true
  tags                          = local.tags
}

resource "aws_s3_bucket" "cloudtrail" {
  bucket        = "${var.project_name}-cloudtrail-${data.aws_caller_identity.current.account_id}"
  force_destroy = true
  tags          = local.tags
}

resource "aws_s3_bucket_policy" "cloudtrail" {
  bucket = aws_s3_bucket.cloudtrail.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "AWSCloudTrailAclCheck"
        Effect    = "Allow"
        Principal = { Service = "cloudtrail.amazonaws.com" }
        Action    = "s3:GetBucketAcl"
        Resource  = aws_s3_bucket.cloudtrail.arn
      },
      {
        Sid       = "AWSCloudTrailWrite"
        Effect    = "Allow"
        Principal = { Service = "cloudtrail.amazonaws.com" }
        Action    = "s3:PutObject"
        Resource  = "${aws_s3_bucket.cloudtrail.arn}/AWSLogs/${data.aws_caller_identity.current.account_id}/*"
        Condition = { StringEquals = { "s3:x-amz-acl" = "bucket-owner-full-control" } }
      }
    ]
  })
}

data "aws_caller_identity" "current" {}

# ── Budget Alerts ─────────────────────────────────────────────────────────────

resource "aws_budgets_budget" "monthly" {
  name         = "${var.project_name}-monthly"
  budget_type  = "COST"
  limit_amount = "50"
  limit_unit   = "USD"
  time_unit    = "MONTHLY"
  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 80
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = ["alerts@getrail.app"]
  }
  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = ["alerts@getrail.app"]
  }
}

# ── Locals ────────────────────────────────────────────────────────────────────

locals {
  env    = var.env != null ? var.env : terraform.workspace
  domain = var.domain != null ? var.domain : (local.env == "prod" ? "api.userail.money" : "api-${local.env}.userail.money")
  tags = {
    Project     = "rail"
    Environment = local.env
    ManagedBy   = "terraform"
  }
}

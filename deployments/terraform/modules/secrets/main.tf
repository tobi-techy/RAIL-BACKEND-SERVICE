resource "aws_secretsmanager_secret" "stack_secrets" {
  name                    = "rail-service-${var.environment}"
  description             = "Secrets for STACK service ${var.environment}"
  recovery_window_in_days = 7
  
  tags = {
    Environment = var.environment
  }
}

resource "aws_secretsmanager_secret_version" "stack_secrets" {
  secret_id = aws_secretsmanager_secret.stack_secrets.id
  secret_string = jsonencode({
    database_url           = var.database_url
    jwt_secret            = var.jwt_secret
    encryption_key        = var.encryption_key
    circle_api_key        = var.circle_api_key
    zerog_storage_key     = var.zerog_storage_key
    zerog_compute_key     = var.zerog_compute_key
    alpaca_api_key        = var.alpaca_api_key
    alpaca_api_secret     = var.alpaca_api_secret
  })
}

resource "aws_iam_policy" "secrets_access" {
  name        = "rail-service-secrets-${var.environment}"
  description = "Allow access to STACK service secrets"
  
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue",
          "secretsmanager:DescribeSecret"
        ]
        Resource = aws_secretsmanager_secret.stack_secrets.arn
      }
    ]
  })
}

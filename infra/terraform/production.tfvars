# Production environment — us-east-1
# Usage: TF_VAR_db_password="..." terraform apply -var-file=production.tfvars
aws_region = "us-east-1"
env        = "prod"
domain     = "api.userail.money"

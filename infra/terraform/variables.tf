variable "aws_region" {
  default = "us-east-1"
}

variable "env" {
  default = "staging"
}

variable "db_password" {
  description = "RDS master password — set via TF_VAR_db_password env var, never hardcode"
  sensitive   = true
}

variable "aws_region" {
  description = "AWS region for infrastructure. Override via tfvars per environment — do NOT change this default."
  default     = "us-east-1"
}

variable "env" {
  default = null # defaults to terraform.workspace
}

variable "db_password" {
  description = "RDS master password — set via TF_VAR_db_password env var, never hardcode"
  sensitive   = true
}

variable "domain" {
  description = "API domain for this environment. Defaults to env-based convention."
  default     = null
}

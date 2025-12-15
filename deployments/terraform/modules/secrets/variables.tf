variable "environment" {
  description = "Environment name"
  type        = string
}

variable "database_url" {
  description = "Database connection URL"
  type        = string
  sensitive   = true
  default     = ""
}

variable "jwt_secret" {
  description = "JWT secret key"
  type        = string
  sensitive   = true
  default     = ""
}

variable "encryption_key" {
  description = "Encryption key"
  type        = string
  sensitive   = true
  default     = ""
}

variable "circle_api_key" {
  description = "Circle API key"
  type        = string
  sensitive   = true
  default     = ""
}

variable "zerog_storage_key" {
  description = "ZeroG storage key"
  type        = string
  sensitive   = true
  default     = ""
}

variable "zerog_compute_key" {
  description = "ZeroG compute key"
  type        = string
  sensitive   = true
  default     = ""
}

variable "alpaca_api_key" {
  description = "Alpaca API key"
  type        = string
  sensitive   = true
  default     = ""
}

variable "alpaca_api_secret" {
  description = "Alpaca API secret"
  type        = string
  sensitive   = true
  default     = ""
}

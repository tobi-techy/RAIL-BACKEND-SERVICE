variable "environment" {
  type = string
}

variable "vpc_id" {
  type = string
}

variable "private_subnet_ids" {
  type = list(string)
}

variable "node_type" {
  type    = string
  default = "cache.t3.micro"
}

variable "num_cache_clusters" {
  type    = number
  default = 2
}

variable "auth_token" {
  type      = string
  sensitive = true
}

variable "allowed_security_groups" {
  type    = list(string)
  default = []
}

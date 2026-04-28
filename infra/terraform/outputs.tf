output "alb_dns" {
  description = "Point your domain's CNAME here"
  value       = aws_lb.main.dns_name
}

output "domain" {
  description = "API domain for this environment"
  value       = local.domain
}

output "acm_cert_validation_records" {
  description = "Add these DNS records in Cloudflare to validate the ACM cert (only needed for new environments)"
  value = {
    for dvo in aws_acm_certificate.api.domain_validation_options : dvo.domain_name => {
      name  = dvo.resource_record_name
      type  = dvo.resource_record_type
      value = dvo.resource_record_value
    }
  }
}

output "ecr_repository_url" {
  description = "Use this in the GitHub Actions workflow"
  value       = aws_ecr_repository.app.repository_url
}

output "rds_endpoint" {
  value     = aws_db_instance.postgres.address
  sensitive = true
}

output "redis_endpoint" {
  value     = aws_elasticache_replication_group.redis.primary_endpoint_address
  sensitive = true
}

output "ecr_url" {
  value = aws_ecr_repository.app.repository_url
}

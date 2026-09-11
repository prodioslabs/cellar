output "alb_dns_name" {
  description = "DNS name of the gateway ALB"
  value       = aws_lb.gateway.dns_name
}

output "alb_arn" {
  description = "ARN of the gateway ALB"
  value       = aws_lb.gateway.arn
}

output "alb_security_group_id" {
  description = "Security group ID of the ALB"
  value       = aws_security_group.alb.id
}

output "target_group_arn" {
  description = "ARN of the gateway target group"
  value       = aws_lb_target_group.gateway.arn
}

output "certificate_arn" {
  description = "ACM certificate ARN used by the HTTPS listener"
  value       = local.certificate_arn
}

output "gateway_url" {
  description = "HTTPS URL for cellar-gateway (domain alias when created, otherwise ALB DNS)"
  value       = local.create_acm && var.create_dns_alias ? "https://${var.acm_domain_name}" : "https://${aws_lb.gateway.dns_name}"
}

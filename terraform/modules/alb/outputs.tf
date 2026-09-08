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

output "gateway_url" {
  description = "HTTPS URL for cellar-gateway via the ALB"
  value       = "https://${aws_lb.gateway.dns_name}"
}

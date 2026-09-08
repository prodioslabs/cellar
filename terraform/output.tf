output "vpc_id" {
  description = "VPC ID"
  value       = module.vpc.vpc_id
}

output "subnet_ids" {
  description = "Public subnet IDs"
  value       = module.vpc.subnet_ids
}

output "leader_public_ip" {
  description = "Public IP of the leader manager"
  value       = module.ec2.leader_public_ip
}

output "leader_private_ip" {
  description = "Private IP of the leader manager"
  value       = module.ec2.leader_private_ip
}

output "manager_private_ips" {
  description = "Private IPs of all managers (leader first)"
  value       = module.ec2.manager_private_ips
}

output "manager_public_ips" {
  description = "Public IPs of all managers (leader first)"
  value       = module.ec2.manager_public_ips
}

output "worker_private_ips" {
  description = "Private IPs of workers"
  value       = module.ec2.worker_private_ips
}

output "worker_public_ips" {
  description = "Public IPs of workers"
  value       = module.ec2.worker_public_ips
}

output "worker_instance_ids" {
  description = "Worker instance IDs"
  value       = module.ec2.worker_instance_ids
}

output "alb_dns_name" {
  description = "DNS name of the gateway ALB"
  value       = module.alb.alb_dns_name
}

output "alb_arn" {
  description = "ARN of the gateway ALB"
  value       = module.alb.alb_arn
}

output "gateway_url" {
  description = "HTTPS URL for cellar-gateway via the ALB"
  value       = module.alb.gateway_url
}

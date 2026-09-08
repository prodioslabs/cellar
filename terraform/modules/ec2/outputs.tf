output "leader_public_ip" {
  description = "Public IP of the leader manager"
  value       = aws_instance.manager[0].public_ip
}

output "leader_private_ip" {
  description = "Private IP of the leader manager"
  value       = aws_instance.manager[0].private_ip
}

output "manager_instance_ids" {
  description = "All manager instance IDs (leader first)"
  value       = aws_instance.manager[*].id
}

output "manager_private_ips" {
  description = "Private IPs of all managers (leader first)"
  value       = aws_instance.manager[*].private_ip
}

output "manager_public_ips" {
  description = "Public IPs of all managers (leader first)"
  value       = aws_instance.manager[*].public_ip
}

output "worker_instance_ids" {
  description = "Worker instance IDs (for ALB target attachments)"
  value       = aws_instance.worker[*].id
}

output "worker_private_ips" {
  description = "Private IPs of workers"
  value       = aws_instance.worker[*].private_ip
}

output "worker_public_ips" {
  description = "Public IPs of workers"
  value       = aws_instance.worker[*].public_ip
}

output "node_security_group_id" {
  description = "Security group ID attached to cluster nodes"
  value       = aws_security_group.node.id
}

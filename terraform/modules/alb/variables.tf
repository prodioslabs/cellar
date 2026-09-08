variable "cluster_name" {
  description = "Cluster name used for resource naming and tags"
  type        = string
}

variable "admin_cidr" {
  description = "CIDR allowed to reach the ALB HTTPS listener"
  type        = string
}

variable "acm_certificate_arn" {
  description = "ARN of an existing ACM certificate in this region for HTTPS:443"
  type        = string
}

variable "idle_timeout" {
  description = "ALB idle timeout in seconds (raise for long log/WebSocket streams)"
  type        = number
}

variable "deregistration_delay" {
  description = "Target group deregistration delay in seconds"
  type        = number
}

variable "vpc_id" {
  description = "VPC ID from the vpc module"
  type        = string
}

variable "subnet_ids" {
  description = "Public subnet IDs from the vpc module"
  type        = list(string)
}

variable "worker_instance_ids" {
  description = "Worker instance IDs from the ec2 module"
  type        = list(string)
}

variable "node_security_group_id" {
  description = "Node security group ID from the ec2 module"
  type        = string
}

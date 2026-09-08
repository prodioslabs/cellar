variable "region" {
  description = "AWS region"
  type        = string
}

variable "cluster_name" {
  description = "Cluster name used for resource naming and tags"
  type        = string
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
}

variable "instance_type" {
  description = "EC2 instance type for all nodes"
  type        = string
}

variable "key_name" {
  description = "Existing EC2 key pair name"
  type        = string
}

variable "admin_cidr" {
  description = "CIDR allowed to reach node and ALB ports"
  type        = string
}

variable "manager_count" {
  description = "Total number of manager nodes (including the leader)"
  type        = number
}

variable "worker_count" {
  description = "Number of worker nodes"
  type        = number
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

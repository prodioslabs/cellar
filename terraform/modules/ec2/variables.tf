variable "region" {
  description = "AWS region"
  type        = string
}

variable "cluster_name" {
  description = "Cluster name used for resource naming and tags"
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
  description = "CIDR allowed to reach node security-group ports"
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

variable "vpc_id" {
  description = "VPC ID from the vpc module"
  type        = string
}

variable "subnet_ids" {
  description = "Public subnet IDs from the vpc module (all AZs)"
  type        = list(string)
}

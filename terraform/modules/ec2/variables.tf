variable "region" {
  description = "AWS region"
  type        = string
}

variable "cluster_name" {
  description = "Cluster name used for resource naming and tags"
  type        = string
}

variable "instance_type" {
  description = "EC2 instance type for all nodes (must support nested virtualization, e.g. m7i.large)"
  type        = string
}

variable "root_volume_size" {
  description = "Root EBS volume size in GiB for each EC2 instance"
  type        = number
}

variable "root_volume_type" {
  description = "Root EBS volume type for each EC2 instance"
  type        = string
}

variable "key_name" {
  description = "Existing EC2 key pair name"
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

variable "subnet_cidrs" {
  description = "Public subnet CIDR blocks from the vpc module (gRPC/Raft ingress)"
  type        = list(string)
}

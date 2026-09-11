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

variable "admin_cidr" {
  description = "CIDR allowed to reach the ALB HTTPS listener (node SG ports are hardcoded to 0.0.0.0/0)"
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
  description = "Existing ACM certificate ARN. Leave empty to create one for acm_domain_name."
  type        = string
  default     = ""
}

variable "acm_domain_name" {
  description = "FQDN for a new ACM certificate (required when acm_certificate_arn is empty)"
  type        = string
  default     = ""
}

variable "route53_zone_id" {
  description = "Route53 hosted zone ID for ACM DNS validation. Empty looks up route53_zone_name / acm_domain_name."
  type        = string
  default     = ""
}

variable "route53_zone_name" {
  description = "Public Route53 zone name when route53_zone_id is empty and a cert is created"
  type        = string
  default     = ""
}

variable "create_dns_alias" {
  description = "When creating a cert, also alias acm_domain_name to the ALB in Route53"
  type        = bool
  default     = true
}

variable "idle_timeout" {
  description = "ALB idle timeout in seconds (raise for long log/WebSocket streams)"
  type        = number
}

variable "deregistration_delay" {
  description = "Target group deregistration delay in seconds"
  type        = number
}

check "acm_inputs" {
  assert {
    condition     = trimspace(var.acm_certificate_arn) != "" || trimspace(var.acm_domain_name) != ""
    error_message = "Provide acm_certificate_arn, or leave it empty and set acm_domain_name to create a certificate."
  }
}

variable "cluster_name" {
  description = "Cluster name used for resource naming and tags"
  type        = string
}

variable "admin_cidr" {
  description = "CIDR allowed to reach the ALB HTTPS listener"
  type        = string
}

variable "acm_certificate_arn" {
  description = "Existing ACM certificate ARN. Leave empty to create a new certificate for acm_domain_name."
  type        = string
  default     = ""
}

variable "acm_domain_name" {
  description = "FQDN for a new ACM certificate (required when acm_certificate_arn is empty)"
  type        = string
  default     = ""
}

variable "route53_zone_id" {
  description = "Route53 hosted zone ID for ACM DNS validation (and optional alias). Empty looks up route53_zone_name / acm_domain_name."
  type        = string
  default     = ""
}

variable "route53_zone_name" {
  description = "Public Route53 zone name used when route53_zone_id is empty and a cert is created"
  type        = string
  default     = ""
}

variable "create_dns_alias" {
  description = "When creating a cert, also create a Route53 alias from acm_domain_name to the ALB"
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

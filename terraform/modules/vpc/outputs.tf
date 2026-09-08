output "vpc_id" {
  description = "VPC ID"
  value       = aws_vpc.this.id
}

output "subnet_ids" {
  description = "Public subnet IDs"
  value       = aws_subnet.public[*].id
}

output "availability_zones" {
  description = "AZs used for public subnets"
  value       = local.azs
}

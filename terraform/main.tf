terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.region
}

module "vpc" {
  source = "./modules/vpc"

  cluster_name = var.cluster_name
  vpc_cidr     = var.vpc_cidr
}

module "ec2" {
  source = "./modules/ec2"

  region        = var.region
  cluster_name  = var.cluster_name
  instance_type = var.instance_type
  key_name      = var.key_name
  admin_cidr    = var.admin_cidr
  manager_count = var.manager_count
  worker_count  = var.worker_count
  vpc_id        = module.vpc.vpc_id
  subnet_ids    = module.vpc.subnet_ids
}

module "alb" {
  source = "./modules/alb"

  cluster_name           = var.cluster_name
  admin_cidr             = var.admin_cidr
  acm_certificate_arn    = var.acm_certificate_arn
  acm_domain_name        = var.acm_domain_name
  route53_zone_id        = var.route53_zone_id
  route53_zone_name      = var.route53_zone_name
  create_dns_alias       = var.create_dns_alias
  idle_timeout           = var.idle_timeout
  deregistration_delay   = var.deregistration_delay
  vpc_id                 = module.vpc.vpc_id
  subnet_ids             = module.vpc.subnet_ids
  worker_instance_ids    = module.ec2.worker_instance_ids
  node_security_group_id = module.ec2.node_security_group_id
}

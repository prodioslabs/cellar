# Cellar AWS Terraform

Single root configuration with child modules under `modules/`.

```text
terraform/
  main.tf
  variables.tf
  output.tf
  terraform.tfvars
  modules/
    vpc/
    ec2/
    alb/
```

All tunable values are set in `terraform.tfvars` (no defaults on root variables).

## Apply

```bash
cd terraform
# edit terraform.tfvars (especially acm_certificate_arn)
terraform init
terraform apply
```

## Networking

- VPC creates **one public subnet per available AZ** in `region`
- Managers and workers are placed **round-robin across all subnets** so every AZ is used

## Defaults in tfvars

- Region: `ap-south-1`
- Cluster name: `Cellar`
- Instance type: `t3a.micro`
- Key pair: existing `codios-aws`
- Nodes: 3 managers + 2 workers
- ALB: HTTPS `:443` (TLS at ALB) → workers HTTP `:8080` (`/readyz`)

## Prerequisites

- AWS credentials with permission to create VPC/EC2/IAM/ELB/SSM resources
- EC2 key pair `codios-aws` in the chosen region
- ACM certificate in the same region

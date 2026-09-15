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

All tunable values are set in `terraform.tfvars`.

## Apply

```bash
cd terraform
# edit terraform.tfvars
terraform init
terraform apply
```

## ACM certificate

Provide **either**:

1. **Existing cert:** set `acm_certificate_arn` to an ACM ARN in the same region, or
2. **Create one:** leave `acm_certificate_arn` empty (`""`) and set:
   - `acm_domain_name` — FQDN for the cert
   - `route53_zone_id` **or** `route53_zone_name` — public hosted zone for DNS validation
   - `create_dns_alias = true` (default) — points the domain at the ALB

## Networking

- VPC creates **one public subnet per available AZ** in `region`
- Managers and workers are placed **round-robin across all subnets**
- Node SG opens SSH/HTTP/HTTPS/gateway (`22`, `80`, `443`, `8080`) to the internet
- Cluster SG opens gRPC/Raft (`17946`, `17947`) to **public subnet CIDRs only**, not the internet

## Prerequisites

- AWS credentials with permission to create VPC/EC2/IAM/ELB/SSM (and ACM/Route53 when creating a cert)
- EC2 key pair named in `key_name` in the chosen region

region               = "ap-south-1"
cluster_name         = "Cellar"
vpc_cidr             = "10.0.0.0/16"
instance_type        = "t3a.micro"
key_name             = "codios-aws"
admin_cidr           = "0.0.0.0/0"
manager_count        = 3
worker_count         = 2
idle_timeout         = 600
deregistration_delay = 300

# Option A: use an existing ACM certificate
acm_certificate_arn = "arn:aws:acm:ap-south-1:689787741512:certificate/55d5353e-d00e-4567-ba9c-e814450e1274"

# Option B: leave acm_certificate_arn empty to create + DNS-validate a cert
# acm_certificate_arn = ""
# acm_domain_name     = "gateway.sandbox.prodioslabs.com"
# route53_zone_name   = "sandbox.prodioslabs.com" # or set route53_zone_id
# create_dns_alias    = true

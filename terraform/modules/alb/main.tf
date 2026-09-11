locals {
  common_tags = {
    Cluster = var.cluster_name
  }

  create_acm = trimspace(var.acm_certificate_arn) == ""

  route53_zone_id = local.create_acm ? (
    trimspace(var.route53_zone_id) != "" ? var.route53_zone_id : data.aws_route53_zone.acm[0].zone_id
  ) : ""

  certificate_arn = local.create_acm ? aws_acm_certificate_validation.gateway[0].certificate_arn : var.acm_certificate_arn
}

check "acm_inputs" {
  assert {
    condition     = !local.create_acm || trimspace(var.acm_domain_name) != ""
    error_message = "Set acm_domain_name when acm_certificate_arn is empty so Terraform can create an ACM certificate."
  }
}

data "aws_route53_zone" "acm" {
  count = local.create_acm && trimspace(var.route53_zone_id) == "" ? 1 : 0

  name         = trimspace(var.route53_zone_name) != "" ? var.route53_zone_name : var.acm_domain_name
  private_zone = false
}

resource "aws_acm_certificate" "gateway" {
  count = local.create_acm ? 1 : 0

  domain_name       = var.acm_domain_name
  validation_method = "DNS"

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-gateway-cert"
  })

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_route53_record" "acm_validation" {
  for_each = local.create_acm ? {
    for dvo in aws_acm_certificate.gateway[0].domain_validation_options : dvo.domain_name => {
      name   = dvo.resource_record_name
      record = dvo.resource_record_value
      type   = dvo.resource_record_type
    }
  } : {}

  allow_overwrite = true
  name            = each.value.name
  records         = [each.value.record]
  ttl             = 60
  type            = each.value.type
  zone_id         = local.route53_zone_id
}

resource "aws_acm_certificate_validation" "gateway" {
  count = local.create_acm ? 1 : 0

  certificate_arn         = aws_acm_certificate.gateway[0].arn
  validation_record_fqdns = [for record in aws_route53_record.acm_validation : record.fqdn]
}

resource "aws_security_group" "alb" {
  name        = "${var.cluster_name}-alb-sg"
  description = "Cellar gateway ALB"
  vpc_id      = var.vpc_id

  ingress {
    description = "HTTPS"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = [var.admin_cidr]
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-alb-sg"
  })
}

resource "aws_security_group_rule" "node_from_alb_gateway" {
  type                     = "ingress"
  description              = "Gateway HTTP from ALB"
  from_port                = 8080
  to_port                  = 8080
  protocol                 = "tcp"
  security_group_id        = var.node_security_group_id
  source_security_group_id = aws_security_group.alb.id
}

resource "aws_lb" "gateway" {
  name               = "${lower(var.cluster_name)}-gateway"
  load_balancer_type = "application"
  internal           = false
  security_groups    = [aws_security_group.alb.id]
  subnets            = var.subnet_ids
  idle_timeout       = var.idle_timeout

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-gateway-alb"
  })
}

resource "aws_lb_target_group" "gateway" {
  name                 = "${lower(var.cluster_name)}-gateway"
  port                 = 8080
  protocol             = "HTTP"
  vpc_id               = var.vpc_id
  target_type          = "instance"
  deregistration_delay = var.deregistration_delay

  health_check {
    enabled             = true
    path                = "/readyz"
    port                = "traffic-port"
    protocol            = "HTTP"
    healthy_threshold   = 2
    unhealthy_threshold = 3
    timeout             = 5
    interval            = 15
    matcher             = "200"
  }

  stickiness {
    enabled = false
    type    = "lb_cookie"
  }

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-gateway-tg"
  })
}

resource "aws_lb_target_group_attachment" "worker" {
  count = length(var.worker_instance_ids)

  target_group_arn = aws_lb_target_group.gateway.arn
  target_id        = var.worker_instance_ids[count.index]
  port             = 8080
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.gateway.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = local.certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.gateway.arn
  }
}

resource "aws_route53_record" "gateway_alias" {
  count = local.create_acm && var.create_dns_alias ? 1 : 0

  zone_id = local.route53_zone_id
  name    = var.acm_domain_name
  type    = "A"

  alias {
    name                   = aws_lb.gateway.dns_name
    zone_id                = aws_lb.gateway.zone_id
    evaluate_target_health = true
  }
}

locals {
  common_tags = {
    Cluster = var.cluster_name
  }
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
  certificate_arn   = var.acm_certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.gateway.arn
  }
}

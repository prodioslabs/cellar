data "aws_ami" "ubuntu" {
  most_recent = true
  owners      = ["099720109477"]

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

data "aws_key_pair" "this" {
  key_name = var.key_name
}

locals {
  ssm_prefix = "/${var.cluster_name}/cellar"

  common_tags = {
    Cluster = var.cluster_name
  }

  # Default node ingress: TCP open to the internet (matches cluster bring-up needs).
  node_ports = {
    ssh     = 22
    http    = 80
    https   = 443
    gateway = 8080
    grpc    = 17946
    raft    = 17947
  }

  node_ingress_cidr = "0.0.0.0/0"

  common_init = <<-EOT
#!/bin/bash
set -euxo pipefail
exec > >(tee -a /var/log/cellar-bootstrap.log) 2>&1
export DEBIAN_FRONTEND=noninteractive
export HOME=/root
export PATH="/usr/local/bin:/usr/bin:/bin:$PATH"
export CELLAR_DATA_DIR=/var/lib/cellar
export CELLAR_COMPONENTS=cellard,cellar,cellar-gateway

apt-get update -y
apt-get install -y unzip curl jq

if ! command -v aws >/dev/null 2>&1; then
  curl -sS "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o /tmp/awscli.zip
  unzip -q /tmp/awscli.zip -d /tmp
  /tmp/aws/install
fi

IMDS=$(curl -sS -X PUT "http://169.254.169.254/latest/api/token" \
  -H "X-aws-ec2-metadata-token-ttl-seconds: 300")
PRIVATE_IP=$(curl -sS -H "X-aws-ec2-metadata-token: $IMDS" \
  http://169.254.169.254/latest/meta-data/local-ipv4)

curl -fsSL https://cellar.prodioslabs.in/install.sh | bash
systemd-sysusers
getent group kvm >/dev/null || groupadd --system kvm
usermod -aG kvm cellar
systemctl daemon-reload
systemctl enable --now cellard

ok=0
for i in $(seq 1 90); do
  if [ -S /var/run/cellar/cellar.sock ] || [ -S /run/cellar/cellar.sock ]; then
    ok=1
    break
  fi
  systemctl is-active --quiet cellard || systemctl status cellard --no-pager || true
  sleep 2
done
if [ "$ok" -ne 1 ]; then
  echo "cellard socket did not appear" >&2
  systemctl status cellard --no-pager || true
  journalctl -u cellard -n 100 --no-pager || true
  exit 1
fi
EOT

  leader_init = <<-EOT
${local.common_init}
cellar init --advertise-addr "$PRIVATE_IP:17946" --raft-addr "$PRIVATE_IP:17947"
MANAGER_TOKEN=$(cellar join-token manager | awk '/--token/{for(i=1;i<=NF;i++) if($i=="--token"){print $(i+1); exit}}')
WORKER_TOKEN=$(cellar join-token worker | awk '/--token/{for(i=1;i<=NF;i++) if($i=="--token"){print $(i+1); exit}}')
test -n "$MANAGER_TOKEN"
test -n "$WORKER_TOKEN"
aws ssm put-parameter --region ${var.region} --overwrite --type SecureString \
  --name "${local.ssm_prefix}/manager-token" --value "$MANAGER_TOKEN"
aws ssm put-parameter --region ${var.region} --overwrite --type SecureString \
  --name "${local.ssm_prefix}/worker-token" --value "$WORKER_TOKEN"
aws ssm put-parameter --region ${var.region} --overwrite --type String \
  --name "${local.ssm_prefix}/leader-addr" --value "$PRIVATE_IP:17946"
echo "leader bootstrap complete"
EOT

  join_init = {
    for role in ["manager", "worker"] : role => <<-EOT
${local.common_init}
until aws ssm get-parameter --region ${var.region} \
  --name "${local.ssm_prefix}/leader-addr" >/dev/null 2>&1; do sleep 10; done
LEADER_ADDR=$(aws ssm get-parameter --region ${var.region} \
  --name "${local.ssm_prefix}/leader-addr" --query Parameter.Value --output text)
JOIN_TOKEN=$(aws ssm get-parameter --region ${var.region} --with-decryption \
  --name "${local.ssm_prefix}/${role}-token" --query Parameter.Value --output text)
test -n "$LEADER_ADDR"
test -n "$JOIN_TOKEN"
%{if role == "manager"}
cellar join --token "$JOIN_TOKEN" \
  --advertise-addr "$PRIVATE_IP:17946" \
  --raft-addr "$PRIVATE_IP:17947" \
  "$LEADER_ADDR"
%{else}
cellar join --token "$JOIN_TOKEN" "$LEADER_ADDR"
# Gateway needs cluster state from a completed join for /readyz.
systemctl enable --now cellar-gateway
systemctl restart cellar-gateway
for i in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:8080/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 2
done
curl -fsS "http://127.0.0.1:8080/healthz" >/dev/null
%{endif}
echo "${role} bootstrap complete"
EOT
  }
}

resource "aws_security_group" "node" {
  name        = "${var.cluster_name}-sg"
  description = "Cellar cluster node traffic"
  vpc_id      = var.vpc_id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-sg"
  })
}

resource "aws_security_group_rule" "node_ingress" {
  for_each = local.node_ports

  type              = "ingress"
  description       = "TCP ${each.key}"
  from_port         = each.value
  to_port           = each.value
  protocol          = "tcp"
  cidr_blocks       = [local.node_ingress_cidr]
  security_group_id = aws_security_group.node.id
}

resource "aws_iam_role" "node" {
  name = "${var.cluster_name}-node"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = local.common_tags
}

resource "aws_iam_role_policy" "node" {
  name = "${var.cluster_name}-node"
  role = aws_iam_role.node.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "ssm:PutParameter",
          "ssm:GetParameter",
          "ssm:GetParameters",
        ]
        Resource = "arn:aws:ssm:${var.region}:*:parameter${local.ssm_prefix}/*"
      },
      {
        Effect = "Allow"
        Action = [
          "kms:Decrypt",
          "kms:Encrypt",
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "kms:ViaService" = "ssm.${var.region}.amazonaws.com"
          }
        }
      },
    ]
  })
}

resource "aws_iam_instance_profile" "node" {
  name = "${var.cluster_name}-node"
  role = aws_iam_role.node.name

  tags = local.common_tags
}

resource "aws_instance" "manager" {
  count = var.manager_count

  ami                    = data.aws_ami.ubuntu.id
  instance_type          = var.instance_type
  key_name               = data.aws_key_pair.this.key_name
  subnet_id              = element(var.subnet_ids, count.index)
  vpc_security_group_ids = [aws_security_group.node.id]
  iam_instance_profile   = aws_iam_instance_profile.node.name
  user_data              = count.index == 0 ? local.leader_init : local.join_init["manager"]
  depends_on             = [aws_security_group.node]

  root_block_device {
    volume_size = var.root_volume_size
    volume_type = var.root_volume_type
  }

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-manager-${count.index + 1}"
    Role = "manager"
  })
}

resource "aws_instance" "worker" {
  count = var.worker_count

  ami           = data.aws_ami.ubuntu.id
  instance_type = var.instance_type
  key_name      = data.aws_key_pair.this.key_name
  # Continue round-robin after managers so nodes cover every regional subnet/AZ.
  subnet_id              = element(var.subnet_ids, count.index + var.manager_count)
  vpc_security_group_ids = [aws_security_group.node.id]
  iam_instance_profile   = aws_iam_instance_profile.node.name
  user_data              = local.join_init["worker"]
  depends_on             = [aws_instance.manager]

  root_block_device {
    volume_size = var.root_volume_size
    volume_type = var.root_volume_type
  }

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-worker-${count.index + 1}"
    Role = "worker"
  })
}

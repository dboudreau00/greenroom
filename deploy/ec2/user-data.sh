#!/bin/bash
# EC2 user data: install Greenroom as a systemd service on Amazon Linux 2023
# or Ubuntu. Set GREENROOM_VERSION and GREENROOM_TOKEN before launch, or
# edit /etc/greenroom/env afterwards. The instance profile should carry
# deploy/ec2/iam-policy.json so ec2 and ssm targets work without keys.
set -euo pipefail

GREENROOM_VERSION="${GREENROOM_VERSION:-latest}"
GREENROOM_TOKEN="${GREENROOM_TOKEN:-$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')}"
ARCH=$(uname -m); case "$ARCH" in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; esac

if [ "$GREENROOM_VERSION" = latest ]; then
  URL="https://github.com/dboudreau00/greenroom/releases/latest/download/greenroom-linux-${ARCH}"
else
  URL="https://github.com/dboudreau00/greenroom/releases/download/${GREENROOM_VERSION}/greenroom-linux-${ARCH}"
fi
curl -fsSL "$URL" -o /usr/local/bin/greenroom
chmod 0755 /usr/local/bin/greenroom

id greenroom >/dev/null 2>&1 || useradd --system --home /opt/greenroom --shell /usr/sbin/nologin greenroom
mkdir -p /opt/greenroom/environments /opt/greenroom/state /etc/greenroom
if [ ! -f /opt/greenroom/greenroom.json ]; then
  /usr/local/bin/greenroom -C /opt/greenroom init >/dev/null
fi
chown -R greenroom:greenroom /opt/greenroom
umask 077
cat > /etc/greenroom/env <<EOF
GREENROOM_TOKEN=${GREENROOM_TOKEN}
AWS_REGION=$(curl -fsS -H "X-aws-ec2-metadata-token: $(curl -fsS -X PUT http://169.254.169.254/latest/api/token -H 'X-aws-ec2-metadata-token-ttl-seconds: 60')" http://169.254.169.254/latest/meta-data/placement/region || true)
EOF

cat > /etc/systemd/system/greenroom.service <<'EOF'
[Unit]
Description=Greenroom demo environment portal
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
User=greenroom
Group=greenroom
SupplementaryGroups=docker
WorkingDirectory=/opt/greenroom
EnvironmentFile=-/etc/greenroom/env
ExecStart=/usr/local/bin/greenroom -C /opt/greenroom serve --listen 0.0.0.0:8470
Restart=always
RestartSec=3
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/opt/greenroom/state
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

getent group docker >/dev/null || groupadd docker
systemctl daemon-reload
systemctl enable --now greenroom
echo "greenroom listening on :8470; token in /etc/greenroom/env"

#!/usr/bin/env bash
# Front the API with Caddy for automatic HTTPS (Let's Encrypt). Run on the box
# AFTER a DNS A record for DOMAIN points at it and ports 80+443 are open:
#   sudo DOMAIN=lemonapi.example.com bash deploy/ec2/tls-setup.sh
# Caddy fetches + auto-renews the cert; the API itself stays plain HTTP on :8080.
set -euo pipefail

DOMAIN="${DOMAIN:?set DOMAIN (an A record pointing at this box; ports 80+443 open)}"
UPSTREAM="${UPSTREAM:-localhost:8080}"
export DEBIAN_FRONTEND=noninteractive

if ! command -v caddy >/dev/null; then
  echo "==> installing Caddy"
  apt-get install -y --no-install-recommends debian-keyring debian-archive-keyring apt-transport-https curl
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
    | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
    > /etc/apt/sources.list.d/caddy-stable.list
  apt-get update -y
  apt-get install -y caddy
fi

echo "==> Caddyfile for ${DOMAIN} → ${UPSTREAM}"
cat > /etc/caddy/Caddyfile <<EOF
${DOMAIN} {
	reverse_proxy ${UPSTREAM}
}
EOF

systemctl restart caddy
sleep 2
systemctl is-active caddy && echo "==> Caddy active; cert provisions on the first HTTPS request to https://${DOMAIN}"

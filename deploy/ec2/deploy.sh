#!/usr/bin/env bash
# Redeploy the API on an already-provisioned box (see setup.sh): pull the latest
# main, rebuild with -tags ORT, restart the service, smoke-check /version.
# Run on the box:  sudo bash /opt/lemon/lemon-search/deploy/ec2/deploy.sh
# Or over SSH:     ssh lemon-box 'sudo bash /opt/lemon/lemon-search/deploy/ec2/deploy.sh'
set -euo pipefail

PREFIX=/opt/lemon
REPO_DIR="$PREFIX/lemon-search"
REPO_REF="${REPO_REF:-main}"
GOBIN=/usr/local/go/bin/go

echo "==> fetch ${REPO_REF}"
# This script runs as root but the repo tree is owned by the lemon service user,
# which git rejects as "dubious ownership" — whitelist it (idempotent).
git config --global --add safe.directory "$REPO_DIR" 2>/dev/null || true
git -C "$REPO_DIR" fetch --depth 1 origin "$REPO_REF"
git -C "$REPO_DIR" checkout -f FETCH_HEAD

echo "==> build"
cd "$REPO_DIR/api"
CGO_ENABLED=1 CGO_LDFLAGS="-L/usr/lib" \
  "$GOBIN" build -tags ORT -trimpath -ldflags='-s -w' -o "$PREFIX/lemon-api.new" ./cmd/api
mv "$PREFIX/lemon-api.new" "$PREFIX/lemon-api"
chown lemon:lemon "$PREFIX/lemon-api"

echo "==> restart"
systemctl restart lemon-api
sleep 2

echo "==> verify"
curl -fsS localhost:8080/readyz && echo
curl -fsS localhost:8080/version && echo
echo "==> deployed $(git -C "$REPO_DIR" rev-parse --short HEAD)"

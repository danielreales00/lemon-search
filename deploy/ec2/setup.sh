#!/usr/bin/env bash
# One-time provisioning for the Lemon Search API on a fresh AWS EC2 c7i.xlarge
# (Ubuntu 24.04 LTS, x86-64), per ADR-0007. Installs Go, the two native libs the
# ORT embedder needs (libonnxruntime + libtokenizers), clones + builds the API
# with -tags ORT, fetches the embedding model, and installs the systemd service.
#
# Idempotent-ish: re-runnable; existing downloads/clone are reused. Run as a
# sudo-capable user:  sudo bash deploy/ec2/setup.sh   (or paste as EC2 user-data).
#
# After it runs, fill /etc/lemon/lemon-api.env (see lemon-api.env.example) with
# the Supabase URL + CORS origin, then: sudo systemctl restart lemon-api.
set -euo pipefail

# --- versions (keep in sync with api/go.mod) ---
GO_VERSION="${GO_VERSION:-1.26.0}"
TOKENIZERS_VERSION="${TOKENIZERS_VERSION:-v1.27.0}"   # daulet/tokenizers (go.mod)
ONNXRUNTIME_VERSION="${ONNXRUNTIME_VERSION:-1.26.0}"  # must expose ORT API ≥ the onnxruntime_go ask (v1.30.1 wants API 25); smoke test below catches a mismatch
REPO_URL="${REPO_URL:-https://github.com/danielreales00/lemon-search.git}"
REPO_REF="${REPO_REF:-main}"

PREFIX=/opt/lemon
REPO_DIR="$PREFIX/lemon-search"
MODEL_DIR="$PREFIX/models/all-MiniLM-L6-v2"
GOBIN=/usr/local/go/bin/go

log() { echo "==> $*"; }

log "apt deps"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends \
  git build-essential curl ca-certificates tzdata postgresql-client

log "Go ${GO_VERSION}"
if ! "$GOBIN" version 2>/dev/null | grep -q "go${GO_VERSION}"; then
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tgz
  rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz
fi

log "libonnxruntime ${ONNXRUNTIME_VERSION} (runtime, dlopen'd)"
if [ ! -e /usr/lib/libonnxruntime.so ]; then
  curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}/onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}.tgz" \
    | tar xz -C /tmp
  cp /tmp/onnxruntime-linux-x64-${ONNXRUNTIME_VERSION}/lib/libonnxruntime.so* /usr/lib/
  ln -sf "/usr/lib/libonnxruntime.so.${ONNXRUNTIME_VERSION}" /usr/lib/libonnxruntime.so
fi

log "libtokenizers ${TOKENIZERS_VERSION} (static, link-time)"
if [ ! -e /usr/lib/libtokenizers.a ]; then
  curl -fsSL "https://github.com/daulet/tokenizers/releases/download/${TOKENIZERS_VERSION}/libtokenizers.linux-amd64.tar.gz" \
    | tar xz -C /usr/lib
fi

log "clone ${REPO_URL}@${REPO_REF}"
mkdir -p "$PREFIX"
if [ -d "$REPO_DIR/.git" ]; then
  git -C "$REPO_DIR" fetch --depth 1 origin "$REPO_REF" && git -C "$REPO_DIR" checkout -f FETCH_HEAD
else
  git clone --depth 1 --branch "$REPO_REF" "$REPO_URL" "$REPO_DIR"
fi

log "embedding model (~86MB)"
mkdir -p "$MODEL_DIR"
for f in model.onnx tokenizer.json config.json tokenizer_config.json vocab.txt special_tokens_map.json; do
  [ -s "$MODEL_DIR/$f" ] || curl -fsSL -o "$MODEL_DIR/$f" \
    "https://huggingface.co/KnightsAnalytics/all-MiniLM-L6-v2/resolve/main/$f"
done

log "build api (-tags ORT, CGO)"
cd "$REPO_DIR/api"
CGO_ENABLED=1 CGO_LDFLAGS="-L/usr/lib" \
  "$GOBIN" build -tags ORT -trimpath -ldflags='-s -w' -o "$PREFIX/lemon-api" ./cmd/api

log "service user + env + unit"
id lemon >/dev/null 2>&1 || useradd --system --home "$PREFIX" --shell /usr/sbin/nologin lemon
mkdir -p /etc/lemon
[ -f /etc/lemon/lemon-api.env ] || {
  sed "s#__MODEL_DIR__#$MODEL_DIR#; s#__REPO_DIR__#$REPO_DIR#" \
    "$REPO_DIR/deploy/ec2/lemon-api.env.example" > /etc/lemon/lemon-api.env
  echo "   wrote /etc/lemon/lemon-api.env — EDIT IT (LEMON_DATABASE_URL, CORS origin)"
}
chown -R lemon:lemon "$PREFIX" /etc/lemon
install -m 644 "$REPO_DIR/deploy/ec2/lemon-api.service" /etc/systemd/system/lemon-api.service
systemctl daemon-reload
systemctl enable lemon-api

log "smoke: validate the ORT lib pairing before declaring success"
cd "$REPO_DIR/api"
if CGO_ENABLED=1 CGO_LDFLAGS="-L/usr/lib" LEMON_ONNX_RUNTIME_DIR=/usr/lib \
   LEMON_ONNX_MODEL_PATH="$MODEL_DIR" \
   "$GOBIN" test -tags "integration ORT" -run TestONNXEmbedderConcurrent ./internal/retrieve/embed/onnx/... 2>&1 | tail -3; then
  log "ORT embed smoke PASSED"
else
  echo "!! ORT smoke FAILED — likely an ONNXRUNTIME_VERSION mismatch with onnxruntime_go (go.mod)." >&2
  echo "   Try a different ONNXRUNTIME_VERSION and re-run." >&2
  exit 1
fi

log "done. Edit /etc/lemon/lemon-api.env, then: sudo systemctl start lemon-api"
log "verify: curl localhost:8080/readyz && curl 'localhost:8080/search?q=sushi'"

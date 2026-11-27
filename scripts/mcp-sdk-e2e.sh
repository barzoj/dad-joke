#!/usr/bin/env bash
# Build the Go server binary, boot it against MongoDB, mint a PAT via signup,
# then run the official @modelcontextprotocol/sdk StreamableHTTP client regression.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

MONGODB_URI="${DAD_JOKE_MONGODB_URI:-mongodb://127.0.0.1:27017}"
DB_NAME="${DAD_JOKE_MONGODB_DATABASE:-dadjoke_mcp_sdk_e2e_$$}"
WORKDIR="$(mktemp -d)"
cleanup() {
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

echo "==> build cmd/server"
CGO_ENABLED=0 go build -o "$WORKDIR/server" ./cmd/server

# Bind an ephemeral port.
PORT_FILE="$WORKDIR/port"
python3 - <<'PY' >"$PORT_FILE"
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
PORT="$(cat "$PORT_FILE")"
BASE_URL="http://127.0.0.1:${PORT}"

echo "==> start server on ${BASE_URL} (db=${DB_NAME})"
DAD_JOKE_MONGODB_URI="$MONGODB_URI" \
DAD_JOKE_MONGODB_DATABASE="$DB_NAME" \
DAD_JOKE_HTTP_ADDR="127.0.0.1:${PORT}" \
COOKIE_SECURE=false \
ALLOW_SIGNUP=true \
  "$WORKDIR/server" >"$WORKDIR/server.log" 2>&1 &
SERVER_PID=$!

echo "==> wait for /healthz"
ok=0
for _ in $(seq 1 60); do
  if curl -fsS "${BASE_URL}/healthz" 2>/dev/null | grep -q '"status":"ok"'; then
    ok=1
    break
  fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "server exited early:" >&2
    cat "$WORKDIR/server.log" >&2 || true
    exit 1
  fi
  sleep 0.25
done
if [[ "$ok" != "1" ]]; then
  echo "healthz timeout:" >&2
  cat "$WORKDIR/server.log" >&2 || true
  exit 1
fi

EMAIL="mcp-sdk-$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')@example.com"
PASS="E2e!pass-$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"
TENANT="mcp-sdk-$(python3 -c 'import uuid; print(uuid.uuid4().hex[:8])')"

echo "==> register + issue PAT"
REG="$(curl -fsS -X POST "${BASE_URL}/v1/auth/register" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"${EMAIL}\",\"password\":\"${PASS}\",\"tenant_name\":\"${TENANT}\"}")"
SESSION="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])' <<<"$REG")"
KEY="$(curl -fsS -X POST "${BASE_URL}/v1/api-keys" \
  -H "Authorization: Bearer ${SESSION}" \
  -H 'Content-Type: application/json' \
  -d '{"name":"mcp-sdk-e2e"}')"
PAT="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])' <<<"$KEY")"

echo "==> npm install @modelcontextprotocol/sdk"
(
  cd e2e/mcp-sdk
  if [[ -f package-lock.json ]]; then
    npm ci --omit=dev
  else
    npm install --omit=dev
  fi
)

echo "==> run StreamableHTTP client regression"
BASE_URL="$BASE_URL" PAT="$PAT" node e2e/mcp-sdk/run.mjs

echo "mcp-sdk-e2e.sh passed"

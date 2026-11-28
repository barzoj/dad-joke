#!/usr/bin/env bash
# End-to-end smoke against a running Docker Compose stack (dev or prod frontend).
# Covers: /healthz → signup/login → PAT + agent token → heartbeat → MCP tools/list
# + list_machines. Exits non-zero on any failure.
#
# Usage:
#   BASE_URL=http://127.0.0.1:8080 ./scripts/smoke-e2e.sh
#   ALLOW_SIGNUP must be true on the target (dev compose default).

set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"
BASE_URL="${BASE_URL%/}"
TIMEOUT_SECS="${TIMEOUT_SECS:-90}"

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 2
  }
}

need_cmd curl
need_cmd python3

json_field() {
  # json_field <json> <python-expr>  e.g. json_field "$body" 'd["access_token"]'
  local raw="$1"
  local expr="$2"
  python3 -c "import json,sys; d=json.loads(sys.argv[1]); print(${expr})" "${raw}"
}

wait_healthz() {
  local deadline=$((SECONDS + TIMEOUT_SECS))
  while ((SECONDS < deadline)); do
    if code=$(curl -sS -o /tmp/dj-smoke-healthz.json -w '%{http_code}' "${BASE_URL}/healthz" || true); then
      if [[ "${code}" == "200" ]]; then
        return 0
      fi
    fi
    sleep 2
  done
  echo "healthz not ready after ${TIMEOUT_SECS}s (last code=${code:-none})" >&2
  exit 1
}

echo "==> waiting for ${BASE_URL}/healthz"
wait_healthz
echo "healthz ok"

SUFFIX="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:10])')"
EMAIL="smoke-${SUFFIX}@example.com"
PASSWORD="Smoke!${SUFFIX}Aa1"
TENANT="smoke-${SUFFIX}"

echo "==> register ${EMAIL}"
REG=$(curl -sS -X POST "${BASE_URL}/v1/auth/register" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"${EMAIL}\",\"password\":\"${PASSWORD}\",\"tenant_name\":\"${TENANT}\"}")
SESSION=$(json_field "${REG}" 'd["access_token"]')
case "${SESSION}" in
  dj_ses_*) ;;
  *) echo "unexpected session token" >&2; exit 1 ;;
esac

echo "==> issue PAT"
PAT_JSON=$(curl -sS -X POST "${BASE_URL}/v1/api-keys" \
  -H "Authorization: Bearer ${SESSION}" \
  -H 'Content-Type: application/json' \
  -d '{"name":"smoke-pat"}')
PAT=$(json_field "${PAT_JSON}" 'd["token"]')
case "${PAT}" in
  dj_pat_*) ;;
  *) echo "unexpected pat" >&2; exit 1 ;;
esac

echo "==> issue agent token"
AGT_JSON=$(curl -sS -X POST "${BASE_URL}/v1/agent-tokens" \
  -H "Authorization: Bearer ${PAT}" \
  -H 'Content-Type: application/json' \
  -d '{"name":"smoke-agent"}')
AGT=$(json_field "${AGT_JSON}" 'd["token"]')
case "${AGT}" in
  dj_agt_*) ;;
  *) echo "unexpected agent token" >&2; exit 1 ;;
esac

MACHINE_ID="$(python3 -c 'import uuid; print(str(uuid.uuid4()))')"
echo "==> heartbeat machine ${MACHINE_ID}"
HB_CODE=$(curl -sS -o /tmp/dj-smoke-hb.json -w '%{http_code}' -X POST "${BASE_URL}/v1/heartbeat" \
  -H "Authorization: Bearer ${AGT}" \
  -H 'Content-Type: application/json' \
  -d "{\"machine_id\":\"${MACHINE_ID}\",\"agent_version\":\"0.0.1-smoke\",\"hostname\":\"smoke\",\"os_version\":\"linux\",\"applied_policy_version\":0,\"time_zone\":\"UTC\",\"utc_offset_minutes\":0}")
if [[ "${HB_CODE}" != "200" ]]; then
  echo "heartbeat failed: ${HB_CODE}" >&2
  cat /tmp/dj-smoke-hb.json >&2 || true
  exit 1
fi

echo "==> MCP tools/list"
MCP_LIST=$(curl -sS -X POST "${BASE_URL}/mcp" \
  -H "Authorization: Bearer ${PAT}" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}')
python3 -c 'import json,sys
raw=sys.argv[1]
d=json.loads(raw)
# Streamable JSON may wrap result under "result"
tools=(d.get("result") or d).get("tools") or []
names={t.get("name") for t in tools}
need={"list_machines","get_machine","set_policy","run_script","get_job","list_events","manage_agent_tokens"}
missing=need-names
if missing:
  raise SystemExit(f"missing tools: {missing}")
print("tools ok:", ",".join(sorted(names)))
' "${MCP_LIST}"

echo "==> MCP list_machines via tools/call"
MCP_MACHINES=$(curl -sS -X POST "${BASE_URL}/mcp" \
  -H "Authorization: Bearer ${PAT}" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_machines","arguments":{}}}')
python3 -c 'import json,sys
raw=sys.argv[1]
mid=sys.argv[2]
d=json.loads(raw)
result=d.get("result") or d
# Prefer structured content text
text=""
for c in (result.get("content") or []):
  if c.get("type")=="text":
    text=c.get("text") or ""
    break
if not text:
  raise SystemExit("empty list_machines content")
payload=json.loads(text)
# MCP structuredContent / content text is an object: {"machines":[...]}
machines=payload.get("machines") if isinstance(payload, dict) else payload
if not isinstance(machines, list):
  raise SystemExit(f"list_machines expected machines array, got {type(machines).__name__}")
sc=result.get("structuredContent")
if sc is not None and not isinstance(sc, dict):
  raise SystemExit(f"structuredContent must be object, got {type(sc).__name__}")
if not any(m.get("machine_id")==mid or m.get("id")==mid for m in machines):
  # MachineSummary uses machine_id
  ids=[m.get("machine_id") or m.get("id") for m in machines]
  if mid not in ids:
    raise SystemExit(f"machine {mid} not in {ids}")
print("list_machines ok")
' "${MCP_MACHINES}" "${MACHINE_ID}"

echo "smoke-e2e passed"

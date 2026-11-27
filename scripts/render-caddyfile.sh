#!/usr/bin/env bash
# Render deploy/Caddyfile with API_DOMAIN / MCP_DOMAIN deduped into one site line.
# Output keeps Caddy env placeholders ({$API_DOMAIN} / {$MCP_DOMAIN}) only.
#
# Usage:
#   API_DOMAIN=api.example.com MCP_DOMAIN=mcp.example.com ./scripts/render-caddyfile.sh
#   API_DOMAIN=app.example.com MCP_DOMAIN=app.example.com ./scripts/render-caddyfile.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMPLATE="${1:-${ROOT}/deploy/Caddyfile}"

API_DOMAIN="${API_DOMAIN:?set API_DOMAIN}"
MCP_DOMAIN="${MCP_DOMAIN:?set MCP_DOMAIN}"

valid_host() {
	[[ "$1" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$ ]]
}

if ! valid_host "${API_DOMAIN}" || ! valid_host "${MCP_DOMAIN}"; then
	echo "render-caddyfile: API_DOMAIN/MCP_DOMAIN must be DNS hostnames" >&2
	exit 2
fi

if [[ "${API_DOMAIN}" == "${MCP_DOMAIN}" ]]; then
	SITE_ADDRESS='{$API_DOMAIN}'
else
	SITE_ADDRESS='{$API_DOMAIN}, {$MCP_DOMAIN}'
fi

if ! grep -qE '^__SITE_ADDRESS__ \{' "${TEMPLATE}"; then
	echo "render-caddyfile: template missing site placeholder line: ${TEMPLATE}" >&2
	exit 2
fi

# Replace only the site-address line (never comment text).
awk -v site="${SITE_ADDRESS}" '
  /^__SITE_ADDRESS__ \{/ { print site " {"; next }
  { print }
' "${TEMPLATE}"

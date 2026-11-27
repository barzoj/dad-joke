#!/bin/sh
# Render deploy/Caddyfile from API_DOMAIN / MCP_DOMAIN (deduped), then exec Caddy.
# Mounted into the caddy service by deploy/compose.prod.yml.
set -eu

API_DOMAIN="${API_DOMAIN:?set API_DOMAIN}"
MCP_DOMAIN="${MCP_DOMAIN:?set MCP_DOMAIN}"
TEMPLATE="${CADDY_TEMPLATE:-/etc/caddy/Caddyfile.tmpl}"
OUT="${CADDY_CONFIG_OUT:-/tmp/Caddyfile}"

valid_host() {
	printf '%s' "$1" | grep -Eq '^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$'
}

if ! valid_host "${API_DOMAIN}" || ! valid_host "${MCP_DOMAIN}"; then
	echo "caddy-entrypoint: API_DOMAIN/MCP_DOMAIN must be DNS hostnames" >&2
	exit 2
fi

# Emit Caddy env placeholders so the runtime file stays parameterized only with
# API_DOMAIN / MCP_DOMAIN (never duplicate the same host twice).
if [ "${API_DOMAIN}" = "${MCP_DOMAIN}" ]; then
	SITE_ADDRESS='{$API_DOMAIN}'
else
	SITE_ADDRESS='{$API_DOMAIN}, {$MCP_DOMAIN}'
fi

awk -v site="${SITE_ADDRESS}" '
  /^__SITE_ADDRESS__ \{/ { print site " {"; next }
  { print }
' "${TEMPLATE}" >"${OUT}"

exec caddy run --config "${OUT}" --adapter caddyfile

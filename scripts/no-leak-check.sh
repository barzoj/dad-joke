#!/usr/bin/env bash
# Automated sterility gate for public deployment templates, scripts, docs, and
# workflows. Fails if private IPs, secret material, opaque production hostnames,
# private-infra repo path patterns, or other forbidden tokens appear.
#
# Intentionally uses only *generic* patterns — never embeds private repository
# names, cloud vendor names, or production hostnames in this file.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

# Forbidden nickname reconstructed at runtime so this file stays sterile.
FORBIDDEN_NICK="$(printf '%s' 'ZGFkZHk=' | base64 -d)"

SCAN_PATHS=(
  deploy
  scripts
  docs
  .github
  README.md
  SECURITY.md
  AGENTS.md
  docker-compose.yml
  Dockerfile
)

mapfile -t FILES < <(
  for p in "${SCAN_PATHS[@]}"; do
    if [[ -f "${p}" ]]; then
      printf '%s\n' "${p}"
    elif [[ -d "${p}" ]]; then
      find "${p}" -type f \
        ! -name '*.png' ! -name '*.jpg' ! -name '*.jpeg' ! -name '*.gif' \
        ! -name '*.webp' ! -name '*.ico' ! -name '*.exe' ! -name '*.zip' \
        ! -name '*.gz' ! -name '*.tar' ! -name '*.woff*' ! -name '*.pdf'
    fi
  done | sort -u
)

FAIL=0
report() {
  local file="$1" line="$2" why="$3"
  printf 'NO-LEAK: %s:%s: %s\n' "${file}" "${line}" "${why}" >&2
  FAIL=1
}

for f in "${FILES[@]}"; do
  # 1) Forbidden nickname / product joke that must never ship in public trees.
  if grep -nEi "${FORBIDDEN_NICK}" -- "${f}" >/dev/null 2>&1; then
    while IFS=: read -r ln _; do
      report "${f}" "${ln}" "forbidden token (nickname)"
    done < <(grep -nEi "${FORBIDDEN_NICK}" -- "${f}" || true)
  fi

  # 2) RFC1918 / link-local private IPv4 (loopback 127.0.0.1 is allowed).
  if grep -nE '([^0-9]|^)(10\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}|192\.168\.[0-9]{1,3}\.[0-9]{1,3}|172\.(1[6-9]|2[0-9]|3[0-1])\.[0-9]{1,3}\.[0-9]{1,3}|169\.254\.[0-9]{1,3}\.[0-9]{1,3})([^0-9]|$)' -- "${f}" >/dev/null 2>&1; then
    while IFS=: read -r ln _; do
      report "${f}" "${ln}" "private/link-local IPv4"
    done < <(grep -nE '([^0-9]|^)(10\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}|192\.168\.[0-9]{1,3}\.[0-9]{1,3}|172\.(1[6-9]|2[0-9]|3[0-1])\.[0-9]{1,3}\.[0-9]{1,3}|169\.254\.[0-9]{1,3}\.[0-9]{1,3})([^0-9]|$)' -- "${f}" || true)
  fi

  # 3) Hardcoded non-example *.co hostnames (example.co allowed).
  if grep -nEi '(^|[^A-Za-z0-9.-])([a-z0-9-]+)\.co([^A-Za-z0-9.-]|$)' -- "${f}" >/dev/null 2>&1; then
    while IFS=: read -r ln content; do
      if echo "${content}" | grep -Eiq '(^|[^A-Za-z0-9.-])example\.co([^A-Za-z0-9.-]|$)'; then
        continue
      fi
      if echo "${content}" | grep -Eiq '(^|[^A-Za-z0-9.-])([a-z0-9-]+)\.co([^A-Za-z0-9.-]|$)'; then
        report "${f}" "${ln}" "hardcoded .co hostname"
      fi
    done < <(grep -nEi '(^|[^A-Za-z0-9.-])([a-z0-9-]+)\.co([^A-Za-z0-9.-]|$)' -- "${f}" || true)
  fi

  # 4) Private-infra repository path pattern on common forges (generic *-infra slug).
  if grep -nEi 'github\.com/[^/]+/[a-z0-9._-]*-infra([/#?]|$)' -- "${f}" >/dev/null 2>&1; then
    while IFS=: read -r ln _; do
      report "${f}" "${ln}" "private-infra repository reference"
    done < <(grep -nEi 'github\.com/[^/]+/[a-z0-9._-]*-infra([/#?]|$)' -- "${f}" || true)
  fi

  # 5) Committed secret material / key prefixes (generic).
  if grep -nE 'BEGIN (RSA |OPENSSH |EC |OPENPGP )?PRIVATE KEY|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}' -- "${f}" >/dev/null 2>&1; then
    while IFS=: read -r ln _; do
      report "${f}" "${ln}" "secret/key material"
    done < <(grep -nE 'BEGIN (RSA |OPENSSH |EC |OPENPGP )?PRIVATE KEY|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}' -- "${f}" || true)
  fi

  # 6) Long live token literals (dj_* with 32+ hex) in scanned public trees.
  if grep -nE 'dj_(pat|agt|ses)_[0-9a-fA-F]{32,}' -- "${f}" >/dev/null 2>&1; then
    while IFS=: read -r ln _; do
      report "${f}" "${ln}" "committed live token literal"
    done < <(grep -nE 'dj_(pat|agt|ses)_[0-9a-fA-F]{32,}' -- "${f}" || true)
  fi
done

if [[ "${FAIL}" -ne 0 ]]; then
  echo "no-leak-check failed" >&2
  exit 1
fi
echo "no-leak-check passed (${#FILES[@]} files)"

#!/usr/bin/env bash
# Cheap docs gate for README.md and docs/*.md (KISS, no new dependencies).
#
# Checks:
#   1) Relative links resolve to existing files; #anchors match GitHub heading slugs
#   2) External https?:// links are syntax-checked only (no network)
#   3) --flags inside <!-- docs-check:install-flags --> … <!-- /docs-check:install-flags -->
#      exist in internal/cli (usage / parseInstallArgs)
#   4) ENV_VAR names inside <!-- docs-check:env-vars --> … <!-- /docs-check:env-vars -->
#      appear in cmd/server/main.go, deploy/env.example, or compose YAML
#   5) README.md has balanced ```mermaid fences
#
# Usage:
#   ./scripts/docs-check.sh
#   DOCS_CHECK_ROOT=/path ./scripts/docs-check.sh   # check a fixture tree
#
# Exit 0 on success; non-zero with every failure listed on stderr.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT="${DOCS_CHECK_ROOT:-${ROOT}}"
cd "${ROOT}"

FAIL=0
FAILURES=()

fail() {
  FAILURES+=("$*")
  FAIL=1
}

github_slug() {
  # Match GitHub: lowercase, drop punctuation (keep spaces/hyphens), each
  # space becomes one hyphen (so "Foo & Bar" → "foo--bar").
  tr '[:upper:]' '[:lower:]' \
    | sed -E 's/<[^>]+>//g' \
    | sed -E 's/`//g' \
    | sed -E "s/'//g" \
    | sed -E 's/[^a-z0-9 _-]//g' \
    | sed -E 's/ /-/g' \
    | sed -E 's/^-+//; s/-+$//'
}

collect_slugs() {
  local file="$1"
  declare -gA SLUGS=()
  declare -A COUNTS=()
  local line heading raw slug n
  while IFS= read -r line || [[ -n "${line}" ]]; do
    if [[ "${line}" =~ ^#{1,6}[[:space:]]+(.*)$ ]]; then
      heading="${BASH_REMATCH[1]}"
      heading="$(printf '%s' "${heading}" | sed -E 's/[[:space:]]+#*$//')"
      raw="$(printf '%s' "${heading}" | github_slug)"
      [[ -z "${raw}" ]] && continue
      n="${COUNTS[${raw}]:-0}"
      if [[ "${n}" -eq 0 ]]; then
        slug="${raw}"
      else
        slug="${raw}-${n}"
      fi
      COUNTS["${raw}"]=$((n + 1))
      SLUGS["${slug}"]=1
    fi
  done <"${file}"
}

check_external_url() {
  local url="$1" file="$2" lineno="$3"
  # Syntax-only: scheme + non-empty host start; no whitespace. Never fetched.
  if [[ ! "${url}" =~ ^https?://[^[:space:]/]+ ]]; then
    fail "${file}:${lineno}: malformed external URL: ${url}"
  fi
  if [[ "${url}" =~ [[:space:]] ]]; then
    fail "${file}:${lineno}: malformed external URL (whitespace): ${url}"
  fi
}

check_anchor() {
  local file="$1" target_file="$2" anchor="$3" lineno="$4"
  if [[ ! -f "${target_file}" ]]; then
    fail "${file}:${lineno}: anchor target file missing: ${target_file}"
    return
  fi
  collect_slugs "${target_file}"
  if [[ -z "${SLUGS[${anchor}]+x}" ]]; then
    fail "${file}:${lineno}: missing heading #${anchor} in ${target_file}"
  fi
}

resolve_rel() {
  local base_dir="$1" rel="$2"
  if command -v realpath >/dev/null 2>&1; then
    realpath -m -- "${base_dir}/${rel}"
  else
    # Fallback without realpath -m.
    (cd "${base_dir}" && python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "${rel}")
  fi
}

scan_links_in_file() {
  local file="$1"
  local lineno=0 line in_fence=0
  local dir
  dir="$(dirname -- "${file}")"

  while IFS= read -r line || [[ -n "${line}" ]]; do
    lineno=$((lineno + 1))
    if [[ "${line}" =~ ^\`\`\` ]]; then
      if [[ "${in_fence}" -eq 0 ]]; then
        in_fence=1
      else
        in_fence=0
      fi
      continue
    fi
    [[ "${in_fence}" -eq 1 ]] && continue

    # Extract destinations from [text](url); skip images ![alt](url).
    # Use grep -o to find candidates, then filter.
    local matches
    matches="$(printf '%s\n' "${line}" | grep -oE '!?[[][^]]*[]][(][^)]+[)]' || true)"
    [[ -z "${matches}" ]] && continue

    local m url
    while IFS= read -r m; do
      [[ -z "${m}" ]] && continue
      # Skip images.
      if [[ "${m}" == '!'* ]]; then
        continue
      fi
      url="${m#*](}"
      url="${url%)}"
      # Strip optional title.
      url="$(printf '%s' "${url}" | sed -E 's/[[:space:]]+".*"[[:space:]]*$//; s/[[:space:]]+'\''.*'\''[[:space:]]*$//; s/^[[:space:]]+//; s/[[:space:]]+$//')"

      if [[ "${url}" =~ ^https?:// ]]; then
        check_external_url "${url}" "${file}" "${lineno}"
        continue
      fi
      if [[ "${url}" =~ ^mailto: ]]; then
        continue
      fi
      if [[ "${url}" =~ ^#(.+)$ ]]; then
        check_anchor "${file}" "${file}" "${BASH_REMATCH[1]}" "${lineno}"
        continue
      fi
      if [[ "${url}" == "#" ]]; then
        fail "${file}:${lineno}: empty anchor"
        continue
      fi

      local path="${url%%#*}"
      local anchor=""
      if [[ "${url}" == *"#"* ]]; then
        anchor="${url#*#}"
      fi
      if [[ "${path}" == /* ]]; then
        fail "${file}:${lineno}: absolute site path not allowed (use relative): ${path}"
        continue
      fi
      [[ -z "${path}" ]] && continue

      local candidate
      candidate="$(resolve_rel "${dir}" "${path}")"
      if [[ ! -e "${candidate}" ]]; then
        fail "${file}:${lineno}: broken relative link: ${path}"
        continue
      fi
      if [[ -n "${anchor}" && -f "${candidate}" ]]; then
        check_anchor "${file}" "${candidate}" "${anchor}" "${lineno}"
      fi
    done <<<"${matches}"
  done <"${file}"
}

extract_marked_section() {
  local file="$1" name="$2"
  local start="<!-- docs-check:${name} -->"
  local end="<!-- /docs-check:${name} -->"
  if ! grep -qF "${start}" -- "${file}"; then
    fail "${file}: missing marker ${start}"
    return 1
  fi
  if ! grep -qF "${end}" -- "${file}"; then
    fail "${file}: missing marker ${end}"
    return 1
  fi
  awk -v s="${start}" -v e="${end}" '
    $0 == s {on=1; next}
    $0 == e {on=0; next}
    on {print}
  ' "${file}"
}

check_install_flags() {
  local section
  if ! section="$(extract_marked_section README.md install-flags)"; then
    return
  fi
  local cli_src="internal/cli/cli.go"
  if [[ ! -f "${cli_src}" ]]; then
    fail "missing ${cli_src}"
    return
  fi
  local flags
  flags="$(printf '%s\n' "${section}" | grep -oE -- '--[a-z0-9][a-z0-9-]*' | sort -u || true)"
  if [[ -z "${flags}" ]]; then
    fail "README.md install-flags section: no --flags found"
    return
  fi
  local f
  while IFS= read -r f; do
    [[ -z "${f}" ]] && continue
    if ! grep -qF -- "${f}" "${cli_src}"; then
      fail "README.md: install flag ${f} not found in ${cli_src}"
    fi
  done <<<"${flags}"
}

check_env_vars() {
  local section
  if ! section="$(extract_marked_section README.md env-vars)"; then
    return
  fi
  local sources=()
  [[ -f cmd/server/main.go ]] && sources+=(cmd/server/main.go)
  [[ -f deploy/env.example ]] && sources+=(deploy/env.example)
  [[ -f docker-compose.yml ]] && sources+=(docker-compose.yml)
  [[ -f deploy/compose.prod.yml ]] && sources+=(deploy/compose.prod.yml)

  local vars
  vars="$(printf '%s\n' "${section}" | grep -oE '\b[A-Z][A-Z0-9]*(_[A-Z0-9]+)+\b' | sort -u || true)"
  if [[ -z "${vars}" ]]; then
    fail "README.md env-vars section: no ENV_VAR names found"
    return
  fi
  local v found src
  while IFS= read -r v; do
    [[ -z "${v}" ]] && continue
    found=0
    for src in "${sources[@]}"; do
      if grep -qF -- "${v}" "${src}"; then
        found=1
        break
      fi
    done
    if [[ "${found}" -eq 0 ]]; then
      fail "README.md: env var ${v} not found in cmd/server/main.go, deploy/env.example, or compose files"
    fi
  done <<<"${vars}"
}

check_mermaid() {
  local file="README.md"
  [[ -f "${file}" ]] || { fail "missing ${file}"; return; }
  local opens=0 in_mermaid=0 in_other=0 lineno=0 line
  while IFS= read -r line || [[ -n "${line}" ]]; do
    lineno=$((lineno + 1))
    if [[ "${line}" =~ ^\`\`\`mermaid[[:space:]]*$ ]]; then
      if [[ "${in_mermaid}" -eq 1 || "${in_other}" -eq 1 ]]; then
        fail "${file}:${lineno}: nested or unclosed fence before \`\`\`mermaid"
      fi
      in_mermaid=1
      opens=$((opens + 1))
      continue
    fi
    if [[ "${line}" =~ ^\`\`\` ]]; then
      if [[ "${in_mermaid}" -eq 1 ]]; then
        in_mermaid=0
        continue
      fi
      if [[ "${in_other}" -eq 1 ]]; then
        in_other=0
      else
        in_other=1
      fi
    fi
  done <"${file}"
  if [[ "${in_mermaid}" -eq 1 ]]; then
    fail "${file}: unclosed \`\`\`mermaid fence"
  fi
  if [[ "${opens}" -lt 1 ]]; then
    fail "${file}: expected at least one \`\`\`mermaid block"
  fi
}

# --- main ---
if [[ ! -f README.md ]]; then
  fail "README.md missing"
else
  scan_links_in_file README.md
  check_install_flags
  check_env_vars
  check_mermaid
fi

shopt -s nullglob
for f in docs/*.md; do
  scan_links_in_file "${f}"
done
shopt -u nullglob

if [[ "${FAIL}" -ne 0 ]]; then
  echo "docs-check failed (${#FAILURES[@]} issue(s)):" >&2
  for i in "${FAILURES[@]}"; do
    echo "  - ${i}" >&2
  done
  exit 1
fi

echo "docs-check passed"

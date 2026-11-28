#!/usr/bin/env bash
# Self-test for scripts/docs-check.sh (bats-free).
# Creates deliberately broken fixtures and asserts the checker fails with
# readable messages; then runs the checker against the real repository.
#
# Usage: ./scripts/docs-check_test.sh

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECK="${ROOT}/scripts/docs-check.sh"
PASS=0
FAIL=0

assert_fails_with() {
  local name="$1"
  local needle="$2"
  shift 2
  local out rc
  set +e
  out="$("$@" 2>&1)"
  rc=$?
  set -e
  if [[ "${rc}" -eq 0 ]]; then
    echo "FAIL: ${name}: expected non-zero exit" >&2
    echo "${out}" >&2
    FAIL=$((FAIL + 1))
    return
  fi
  if ! printf '%s\n' "${out}" | grep -qF -- "${needle}"; then
    echo "FAIL: ${name}: expected output to contain: ${needle}" >&2
    echo "${out}" >&2
    FAIL=$((FAIL + 1))
    return
  fi
  echo "ok: ${name}"
  PASS=$((PASS + 1))
}

assert_passes() {
  local name="$1"
  shift
  local out rc
  set +e
  out="$("$@" 2>&1)"
  rc=$?
  set -e
  if [[ "${rc}" -ne 0 ]]; then
    echo "FAIL: ${name}: expected exit 0" >&2
    echo "${out}" >&2
    FAIL=$((FAIL + 1))
    return
  fi
  echo "ok: ${name}"
  PASS=$((PASS + 1))
}

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

# Minimal skeleton so the checker can look up cli/env sources.
mkdir -p "${TMP}/internal/cli" "${TMP}/cmd/server" "${TMP}/deploy" "${TMP}/docs"
cat >"${TMP}/internal/cli/cli.go" <<'EOF'
const usageText = `Usage: dad-joke <command> [flags]
  install [--token SECRET] [--backend-url URL] [--heartbeat-interval SECONDS]
`
EOF
cat >"${TMP}/cmd/server/main.go" <<'EOF'
uri := os.Getenv("DAD_JOKE_MONGODB_URI")
db := os.Getenv("DAD_JOKE_MONGODB_DATABASE")
addr := envOr("DAD_JOKE_HTTP_ADDR", ":8080")
allowSignup := envBool("ALLOW_SIGNUP", true)
cookieSecure := envBool("COOKIE_SECURE", true)
pass := os.Getenv("DAD_JOKE_CREATE_USER_PASSWORD")
EOF
cat >"${TMP}/deploy/env.example" <<'EOF'
API_DOMAIN=api.example.com
MCP_DOMAIN=mcp.example.com
IMAGE_TAG=latest
DAD_JOKE_MONGODB_DATABASE=dadjoke
ALLOW_SIGNUP=false
EOF
cat >"${TMP}/docker-compose.yml" <<'EOF'
services: {}
EOF
cat >"${TMP}/deploy/compose.prod.yml" <<'EOF'
services: {}
EOF
cat >"${TMP}/docs/ARCHITECTURE.md" <<'EOF'
# Architecture

## Privilege model
EOF

# --- Fixture A: broken relative link ---
cat >"${TMP}/README.md" <<'EOF'
# Dad Joke

See [missing](docs/DOES_NOT_EXIST.md).

```mermaid
flowchart LR
  A --> B
```

<!-- docs-check:install-flags -->
`install --token X --backend-url Y --heartbeat-interval 60`
<!-- /docs-check:install-flags -->

<!-- docs-check:env-vars -->
| `DAD_JOKE_MONGODB_URI` | default |
| `ALLOW_SIGNUP` | false |
<!-- /docs-check:env-vars -->
EOF
assert_fails_with "broken relative link" "broken relative link" \
  env DOCS_CHECK_ROOT="${TMP}" "${CHECK}"

# --- Fixture B: missing heading anchor ---
cat >"${TMP}/README.md" <<'EOF'
# Dad Joke

Link to [nope](#this-heading-does-not-exist).

Also [arch](docs/ARCHITECTURE.md#missing-section).

```mermaid
flowchart LR
  A --> B
```

<!-- docs-check:install-flags -->
`install --token X --backend-url Y --heartbeat-interval 60`
<!-- /docs-check:install-flags -->

<!-- docs-check:env-vars -->
| `DAD_JOKE_MONGODB_URI` | default |
| `ALLOW_SIGNUP` | false |
<!-- /docs-check:env-vars -->
EOF
assert_fails_with "missing #anchor" "missing heading" \
  env DOCS_CHECK_ROOT="${TMP}" "${CHECK}"

# --- Fixture C: unknown install flag ---
cat >"${TMP}/README.md" <<'EOF'
# Dad Joke

```mermaid
flowchart LR
  A --> B
```

<!-- docs-check:install-flags -->
`install --token X --backend-url Y --heartbeat-interval 60 --bogus-flag Z`
<!-- /docs-check:install-flags -->

<!-- docs-check:env-vars -->
| `DAD_JOKE_MONGODB_URI` | default |
| `ALLOW_SIGNUP` | false |
<!-- /docs-check:env-vars -->
EOF
assert_fails_with "unknown install flag" "--bogus-flag" \
  env DOCS_CHECK_ROOT="${TMP}" "${CHECK}"

# --- Fixture D: unknown env var ---
cat >"${TMP}/README.md" <<'EOF'
# Dad Joke

```mermaid
flowchart LR
  A --> B
```

<!-- docs-check:install-flags -->
`install --token X --backend-url Y --heartbeat-interval 60`
<!-- /docs-check:install-flags -->

<!-- docs-check:env-vars -->
| `DAD_JOKE_MONGODB_URI` | default |
| `FOO_BAR` | unknown |
<!-- /docs-check:env-vars -->
EOF
assert_fails_with "unknown env var" "FOO_BAR" \
  env DOCS_CHECK_ROOT="${TMP}" "${CHECK}"

# --- Fixture E: unclosed mermaid ---
cat >"${TMP}/README.md" <<'EOF'
# Dad Joke

```mermaid
flowchart LR
  A --> B

<!-- docs-check:install-flags -->
`install --token X --backend-url Y --heartbeat-interval 60`
<!-- /docs-check:install-flags -->

<!-- docs-check:env-vars -->
| `DAD_JOKE_MONGODB_URI` | default |
| `ALLOW_SIGNUP` | false |
<!-- /docs-check:env-vars -->
EOF
assert_fails_with "unclosed mermaid" "unclosed" \
  env DOCS_CHECK_ROOT="${TMP}" "${CHECK}"

# --- Fixture F: multiple failures reported in one run ---
cat >"${TMP}/README.md" <<'EOF'
# Dad Joke

See [missing](docs/DOES_NOT_EXIST.md).

```mermaid
flowchart LR
  A --> B
```

<!-- docs-check:install-flags -->
`install --token X --bogus-flag Z`
| `--bogus-flag` | bad |
<!-- /docs-check:install-flags -->

<!-- docs-check:env-vars -->
| `FOO_BAR` | unknown |
<!-- /docs-check:env-vars -->
EOF
{
  set +e
  multi_out="$(env DOCS_CHECK_ROOT="${TMP}" "${CHECK}" 2>&1)"
  multi_rc=$?
  set -e
  if [[ "${multi_rc}" -eq 0 ]]; then
    echo "FAIL: multiple failures: expected non-zero exit" >&2
    echo "${multi_out}" >&2
    FAIL=$((FAIL + 1))
  else
    missing=0
    for needle in "broken relative link" "--bogus-flag" "FOO_BAR"; do
      if ! printf '%s\n' "${multi_out}" | grep -qF -- "${needle}"; then
        echo "FAIL: multiple failures: missing needle: ${needle}" >&2
        missing=1
      fi
    done
    if [[ "${missing}" -eq 1 ]]; then
      echo "${multi_out}" >&2
      FAIL=$((FAIL + 1))
    else
      echo "ok: multiple failures reported together"
      PASS=$((PASS + 1))
    fi
  fi
}

# --- Fixture G: clean tree passes ---
cat >"${TMP}/README.md" <<'EOF'
# Dad Joke

See [Architecture](docs/ARCHITECTURE.md#architecture) and [Privilege](docs/ARCHITECTURE.md#privilege-model).

Link to self: [top](#dad-joke).

External (not fetched): [example](https://example.com/path).

Compose uses `--env-file` outside the install-flags marker (ignored).

```mermaid
flowchart LR
  A[Agent] -->|AgentAPI| S[Server]
```

<!-- docs-check:install-flags -->
```powershell
.\dad-joke-windows-amd64.exe install --token dj_agt_example --backend-url https://api.your-server.example --heartbeat-interval 60
```
| Flag | Notes |
| --- | --- |
| `--token` | agent token |
| `--backend-url` | API base |
| `--heartbeat-interval` | seconds |
<!-- /docs-check:install-flags -->

<!-- docs-check:env-vars -->
| Variable | Default |
| --- | --- |
| `DAD_JOKE_MONGODB_URI` | `mongodb://127.0.0.1:27017` |
| `DAD_JOKE_MONGODB_DATABASE` | `dadjoke` |
| `DAD_JOKE_HTTP_ADDR` | `:8080` |
| `ALLOW_SIGNUP` | `true` |
| `COOKIE_SECURE` | `true` |
| `DAD_JOKE_CREATE_USER_PASSWORD` | (empty) |
| `API_DOMAIN` | (required in prod `.env`) |
| `MCP_DOMAIN` | (required in prod `.env`) |
| `IMAGE_TAG` | `latest` |
<!-- /docs-check:env-vars -->
EOF
assert_passes "clean fixture" \
  env DOCS_CHECK_ROOT="${TMP}" "${CHECK}"

# --- Real repository must pass (after README rewrite lands) ---
# When run before the polished README exists, this may fail; the CI job always
# runs after the rewrite. Here we still invoke it so a green tree is required.
assert_passes "real repository" "${CHECK}"

echo
echo "docs-check_test: ${PASS} passed, ${FAIL} failed"
if [[ "${FAIL}" -ne 0 ]]; then
  exit 1
fi

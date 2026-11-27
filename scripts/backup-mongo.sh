#!/usr/bin/env bash
# MongoDB logical backup for the self-host compose stack.
# Usage:
#   MONGO_CONTAINER=dad-joke-mongo-1 ./scripts/backup-mongo.sh
#   BACKUP_DIR=/var/backups/dad-joke RETENTION_DAYS=14 ./scripts/backup-mongo.sh
#
# Zero hardcoded secrets: connection details come from the running container
# and/or environment variables you supply at runtime.

set -euo pipefail

BACKUP_DIR="${BACKUP_DIR:-./backups/mongo}"
RETENTION_DAYS="${RETENTION_DAYS:-7}"
MONGO_CONTAINER="${MONGO_CONTAINER:-}"
MONGO_URI="${MONGO_URI:-}"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
ARCHIVE="${BACKUP_DIR}/dadjoke-${TIMESTAMP}.archive.gz"

mkdir -p "${BACKUP_DIR}"

if [[ -n "${MONGO_CONTAINER}" ]]; then
  # Dump from a running compose/service container (no credentials in this script).
  docker exec "${MONGO_CONTAINER}" mongodump --archive --gzip >"${ARCHIVE}"
elif [[ -n "${MONGO_URI}" ]]; then
  mongodump --uri="${MONGO_URI}" --archive --gzip >"${ARCHIVE}"
else
  echo "set MONGO_CONTAINER (docker exec) or MONGO_URI (local mongodump)" >&2
  exit 2
fi

# Retention pruning: delete archives older than RETENTION_DAYS.
find "${BACKUP_DIR}" -type f -name 'dadjoke-*.archive.gz' -mtime "+${RETENTION_DAYS}" -print -delete

echo "wrote ${ARCHIVE}"

#!/usr/bin/env bash

# Resets the database by wiping all data and re-running migrations.
# Usage: ./scripts/db_reset.sh [--force] [--skip-migrate] [--seed]

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/.." && pwd)

if [[ -f "${REPO_ROOT}/.env" ]]; then
  # shellcheck source=/dev/null
  source "${REPO_ROOT}/.env"
fi

DATABASE_URL=${DATABASE_URL:-}

if [[ -z "${DATABASE_URL}" ]]; then
  echo "DATABASE_URL is not set. Export it or add it to .env before running this script." >&2
  exit 1
fi

FORCE=false
RUN_MIGRATIONS=true
RUN_SEED=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --force|-f)
      FORCE=true
      shift
      ;;
    --skip-migrate)
      RUN_MIGRATIONS=false
      shift
      ;;
    --seed)
      RUN_SEED=true
      shift
      ;;
    --help|-h)
      cat <<'USAGE'
Usage: ./scripts/db_reset.sh [options]

Drops all data, recreates the schema, and applies migrations for the database specified by DATABASE_URL.

Options:
  -f, --force        Skip the interactive confirmation prompt.
      --skip-migrate Skip rerunning migrations after wiping the database.
      --seed         Run `go run scripts/seed.go` after migrations (requires seed.go).
  -h, --help         Show this help message and exit.
USAGE
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      exit 1
      ;;
  esac
done

if [[ ! -x "${SCRIPT_DIR}/db_wipe.sh" ]]; then
  echo "db_wipe.sh is missing or not executable. Expected at ${SCRIPT_DIR}/db_wipe.sh" >&2
  exit 1
fi

if [[ "${FORCE}" == true ]]; then
  bash "${SCRIPT_DIR}/db_wipe.sh" --force
else
  bash "${SCRIPT_DIR}/db_wipe.sh"
fi

if [[ "${RUN_MIGRATIONS}" == true ]]; then
  if ! command -v migrate >/dev/null 2>&1; then
    echo "migrate CLI not found on PATH. Install golang-migrate to re-run migrations." >&2
    exit 1
  fi

  echo "Applying migrations..."
  migrate -path "${REPO_ROOT}/migrations" -database "${DATABASE_URL}" up
  echo "Migrations complete."
else
  echo "Skipping migrations as requested."
fi

if [[ "${RUN_SEED}" == true ]]; then
  if [[ ! -f "${REPO_ROOT}/scripts/seed.go" ]]; then
    echo "Seed file scripts/seed.go not found. Skipping seeding step." >&2
    exit 0
  fi

  echo "Running database seed..."
  (cd "${REPO_ROOT}" && go run scripts/seed.go)
  echo "Seeding complete."
fi

echo "Database reset complete."

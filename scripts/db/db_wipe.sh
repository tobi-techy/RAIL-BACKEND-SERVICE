#!/usr/bin/env bash

# Wipes the Postgres database defined by DATABASE_URL by dropping and recreating the public schema.
# Usage: ./scripts/db_wipe.sh [--force]

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)

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

while [[ $# -gt 0 ]]; do
  case "$1" in
    --force|-f)
      FORCE=true
      shift
      ;;
    --help|-h)
      cat <<'USAGE'
Usage: ./scripts/db_wipe.sh [--force]

Drops and recreates the public schema for the database specified by DATABASE_URL.

Options:
  -f, --force   Skip the interactive confirmation prompt.
  -h, --help    Show this help message and exit.
USAGE
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      exit 1
      ;;
  esac
done

if ! command -v psql >/dev/null 2>&1; then
  echo "psql not found on PATH. Install the PostgreSQL client tools to continue." >&2
  exit 1
fi

if [[ "${FORCE}" == false ]]; then
  read -r -p "This will permanently delete ALL data in ${DATABASE_URL}. Continue? [y/N] " reply
  if [[ ! "${reply}" =~ ^[Yy]$ ]]; then
    echo "Aborted."
    exit 0
  fi
fi

DB_OWNER=$(psql "${DATABASE_URL}" -Atq --no-psqlrc -c "SELECT current_user;")
DB_OWNER=${DB_OWNER//[[:space:]]/}

if [[ -z "${DB_OWNER}" ]]; then
  echo "Unable to determine database user. Aborting for safety." >&2
  exit 1
fi

echo "Dropping and recreating public schema as ${DB_OWNER}..."
psql "${DATABASE_URL}" --no-psqlrc -v ON_ERROR_STOP=1 <<SQL
DROP SCHEMA public CASCADE;
CREATE SCHEMA public AUTHORIZATION "${DB_OWNER}";
GRANT ALL ON SCHEMA public TO "${DB_OWNER}";
GRANT ALL ON SCHEMA public TO public;
COMMENT ON SCHEMA public IS 'standard public schema';
SQL

echo "Database wipe complete."

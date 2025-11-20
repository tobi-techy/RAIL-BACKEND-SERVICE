#!/usr/bin/env bash

# Minimal script to clear entire database
set -e

if [[ -f .env ]]; then
  source .env
fi

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "DATABASE_URL not set"
  exit 1
fi

psql "${DATABASE_URL}" -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"
echo "Database cleared"

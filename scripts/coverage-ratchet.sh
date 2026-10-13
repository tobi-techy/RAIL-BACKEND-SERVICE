#!/usr/bin/env bash
#
# Coverage ratchet: coverage may never go down.
#
# The aspiration for this repository is COVERAGE_TARGET (default 40%, see
# docs/coverage.md). Gating on it directly fails on every pull request — the
# suite measures 13.2% — and a gate that is always red is a gate people learn to
# ignore, which is how this repository got to 13.2% unnoticed.
#
# This script records what the suite actually achieves in coverage-floor.txt and
# fails only when coverage drops below it. The floor only ever rises; the 40%
# target stays named in both the passing and the failing output so the gap is
# visible rather than hidden. The shape is the one the miriam repository already
# uses for its mypy baseline (scripts/mypy_ratchet.py).
#
# Usage:
#   scripts/coverage-ratchet.sh coverage.out            # check (exit 1 on a drop)
#   scripts/coverage-ratchet.sh coverage.out --update   # raise the floor to now

set -euo pipefail

PROFILE="${1:?usage: coverage-ratchet.sh <coverprofile> [--update]}"
FLOOR_FILE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/coverage-floor.txt"
TARGET="${COVERAGE_TARGET:-40}"

if [[ ! -f "$PROFILE" ]]; then
  echo "coverage profile ${PROFILE} not found" >&2
  exit 1
fi
if [[ ! -f "$FLOOR_FILE" ]]; then
  echo "coverage floor file ${FLOOR_FILE} not found" >&2
  exit 1
fi

CURRENT="$(go tool cover -func="${PROFILE}" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
if [[ -z "${CURRENT}" ]]; then
  echo "could not read the total from ${PROFILE}" >&2
  exit 1
fi
FLOOR="$(tr -d '[:space:]' < "${FLOOR_FILE}")"

if [[ "${2:-}" == "--update" ]]; then
  printf '%s\n' "${CURRENT}" > "${FLOOR_FILE}"
  echo "coverage floor raised to ${CURRENT}% (target ${TARGET}%)"
  exit 0
fi

echo "Coverage: ${CURRENT}% (floor ${FLOOR}%, target ${TARGET}%)"

if awk -v c="${CURRENT}" -v f="${FLOOR}" 'BEGIN { exit !(c + 0 < f + 0) }'; then
  echo "Coverage check failed (${CURRENT}% < ${FLOOR}% floor; target ${TARGET}%)."
  echo "The floor only rises. Cover the code you added or changed, or bring the"
  echo "drop to the owner — do not lower $(basename "${FLOOR_FILE}")."
  exit 1
fi

if awk -v c="${CURRENT}" -v f="${FLOOR}" 'BEGIN { exit !(c + 0 > f + 0) }'; then
  echo "Coverage rose from ${FLOOR}% to ${CURRENT}% (target ${TARGET}%)."
  echo "Lock it in: scripts/coverage-ratchet.sh ${PROFILE} --update"
  exit 0
fi

echo "Coverage check passed (${CURRENT}% >= ${FLOOR}% floor; target ${TARGET}%)."

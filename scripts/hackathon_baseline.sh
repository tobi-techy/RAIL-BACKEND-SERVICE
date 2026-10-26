#!/usr/bin/env bash
# Tag the Meta hackathon baseline at 17:00 WAT on 26 Oct 2026.
#
# Tags the default branch (origin/main by default) so it works from a feature
# branch with a dirty working tree. Pushes the tag so judges can diff from it.
#
# Usage:
#   scripts/hackathon_baseline.sh              # tag origin/main
#   scripts/hackathon_baseline.sh origin/foo   # tag another ref
#   scripts/hackathon_baseline.sh --force      # allow before 17:00 WAT
#
# Never delete or move the tag afterwards. If it is wrong, tell Tobi.

set -euo pipefail

TAG="${TAG:-hackathon-baseline}"
OPEN_AT="2026-10-26T17:00"
REF="origin/main"
FORCE=""

for arg in "$@"; do
  case "$arg" in
    --force) FORCE="1" ;;
    -*) echo "unknown flag: $arg" >&2; exit 2 ;;
    *) REF="$arg" ;;
  esac
done

git rev-parse --is-inside-work-tree >/dev/null 2>&1 || {
  echo "not a git repository" >&2
  exit 1
}

NOW=$(TZ=Africa/Lagos date +%Y-%m-%dT%H:%M)
if [[ -z "$FORCE" && "$NOW" < "$OPEN_AT" ]]; then
  echo "It is $NOW WAT. The window opens at $OPEN_AT WAT." >&2
  echo "Tagging now would exclude work you are about to do. Re-run with --force to override." >&2
  exit 1
fi

if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
  echo "tag $TAG already exists at $(git rev-parse --short "$TAG"^{commit})." >&2
  echo "Baselines are not moved. If it points at the wrong commit, tell Tobi." >&2
  exit 1
fi

if git remote get-url origin >/dev/null 2>&1; then
  echo "fetching origin..."
  git fetch --quiet origin
fi

git rev-parse -q --verify "$REF" >/dev/null || {
  echo "ref not found: $REF" >&2
  exit 1
}

COMMIT=$(git rev-parse "$REF^{commit}")
SHORT=$(git rev-parse --short "$REF^{commit}")

git tag -a "$TAG" "$REF" -m "Meta hackathon baseline: window opens $OPEN_AT WAT"

if [[ -n "$(git status --porcelain)" ]]; then
  echo "note: working tree has uncommitted changes; they are not part of the tag."
fi

git push origin "$TAG"

echo
echo "tagged $TAG -> $SHORT ($COMMIT)"
echo "record this hash in HACKATHON.md:"
echo "  git log -1 --format='%h %ad %s' --date=iso $TAG"
echo
echo "to review the submission later:"
echo "  git log --oneline $TAG..HEAD"
echo "  git diff --stat $TAG..HEAD"

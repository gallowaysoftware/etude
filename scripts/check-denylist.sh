#!/bin/sh
# Denylist tripwire (docs/excision-checklist.md step 12): fail if personal
# identifiers leak into the tracked tree. Exclusions:
#   - gallowaysoftware / "Galloway Software" — the public brand, not a leak
#   - docs/excision-checklist.md — documents the patterns themselves
#   - this script — contains the pattern literally
#   - go.sum — upstream hashes/paths we do not control
# Only tracked files are scanned (git grep): ignored paths are not public.
set -eu

cd "$(git rev-parse --show-toplevel)"

hits=$(git grep -nIEi 'kyle|galloway|pequalsnp|thegalloways' -- \
  . ':!go.sum' ':!docs/excision-checklist.md' ':!scripts/check-denylist.sh' \
  | grep -viE 'galloway ?software' || true)

if [ -n "$hits" ]; then
  echo "denylist tripwire: personal identifiers found in tracked files:" >&2
  echo "$hits" >&2
  exit 1
fi
echo "denylist tripwire: ok — no personal identifiers in tracked files"

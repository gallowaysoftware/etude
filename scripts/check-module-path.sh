#!/bin/sh
# Assert go.mod's module path matches the repo's git remote (issue #19).
# A rename is exactly when these drift, and the failure is quiet: everything
# builds locally while `go install <tag>` fails a path check for everyone else.
#
# Usage:
#   scripts/check-module-path.sh [owner/repo]
#
# With an argument (CI passes ${{ github.repository }}) that is authoritative;
# without one the origin remote is parsed (git@, https://, and ssh:// forms).
set -eu

cd "$(git rev-parse --show-toplevel)"

module_path=$(sed -n 's/^module[[:space:]]\{1,\}\([^[:space:]]\{1,\}\).*/\1/p' go.mod | head -n 1)
if [ -z "$module_path" ]; then
  echo "check-module-path: no module line found in go.mod" >&2
  exit 2
fi

if [ $# -ge 1 ]; then
  expected="github.com/$1"
else
  url=$(git remote get-url origin 2>/dev/null) || {
    echo "check-module-path: no argument given and no 'origin' remote to compare against" >&2
    exit 2
  }
  case $url in
    *://*)
      # https://github.com/owner/repo(.git) or ssh://git@github.com/owner/repo(.git)
      rest=${url#*://}
      host=${rest%%/*}
      host=${host#*@}
      path=${rest#*/}
      ;;
    *@*:*)
      # git@github.com:owner/repo(.git)
      rest=${url#*@}
      host=${rest%%:*}
      path=${rest#*:}
      ;;
    *)
      echo "check-module-path: unrecognised remote URL: $url" >&2
      exit 2
      ;;
  esac
  expected="$host/${path%.git}"
fi

if [ "$module_path" != "$expected" ]; then
  echo "check-module-path: go.mod declares '$module_path' but the remote is '$expected'" >&2
  echo "rename drift: fix go.mod (and imports) or push to the matching remote" >&2
  exit 1
fi
echo "check-module-path: ok — '$module_path' matches remote"

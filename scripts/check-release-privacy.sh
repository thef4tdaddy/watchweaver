#!/bin/sh
set -eu

tracked="$(git ls-files)"

for pattern in '*.db' '*.db-shm' '*.db-wal' '*.sqlite' '*.sqlite3' '*.csv' '*.log'; do
  if printf '%s\n' "$tracked" | grep -E "$(printf '%s' "$pattern" | sed 's/\./\\./g; s/\*/.*/g')$" >/dev/null; then
    echo "tracked private/runtime artifact matches $pattern" >&2
    exit 1
  fi
done

if printf '%s\n' "$tracked" | grep -E '(^|/)\.env($|\.)' | grep -v '^\.env\.example$' >/dev/null; then
  echo "tracked environment file found" >&2
  exit 1
fi

findings="$(git grep -nE "(^|[[:space:]=\"'])(/Users/|/home/)|[A-Za-z]:\\\\Users\\\\|discord(app)?\\.com/api/webhooks/[0-9]+/[^[:space:]]+" -- ':!scripts/check-release-privacy.sh' || true)"
if [ -n "$findings" ]; then
  printf '%s\n' "$findings" >&2
  echo "possible personal path or live Discord webhook found in tracked files" >&2
  exit 1
fi

echo "tracked release examples and artifacts are sanitized"

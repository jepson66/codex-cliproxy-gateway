#!/bin/sh
set -eu

pattern="(sk-[A-Za-z0-9_-]{20,}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|-----BEGIN ([A-Z ]+ )?PRIVATE KEY-----|[Aa][Pp][Ii][_-]?[Kk][Ee][Yy][[:space:]]*[:=][[:space:]]*[^[:space:]]{24,})"

matches=$(git grep -Il -E "$pattern" -- . ':(exclude)*_test.go' || true)
if [ -n "$matches" ]; then
  printf '%s\n' 'Potential secrets detected in tracked files:' >&2
  printf '%s\n' "$matches" >&2
  printf '%s\n' 'Only filenames are shown. Remove or replace the secret before committing.' >&2
  exit 1
fi

printf '%s\n' 'No high-confidence secret patterns found in tracked non-test files.'

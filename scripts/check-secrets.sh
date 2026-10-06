#!/usr/bin/env bash
# Secret scanner (basic, hermetic — no external binaries).
#
# Fails the build when a high-confidence secret or a TLS-verification bypass is
# found in tracked files. Runs in CI; run it locally with `make secrets`.
#
# It deliberately errs toward high-confidence patterns so it does not become
# noise: add rules here rather than weakening existing ones.
set -uo pipefail
cd "$(dirname "$0")/.."

patterns=(
  # Private keys
  "-----BEGIN [A-Z ]*PRIVATE KEY-----"
  # GitHub tokens
  "gh[pousr]_[A-Za-z0-9]{36}"
  "github_pat_[A-Za-z0-9_]{22,}"
  # AWS access key ids
  "AKIA[0-9A-Z]{16}"
  # Slack tokens
  "xox[baprs]-[A-Za-z0-9-]{10,}"
  # A real value assigned to one of our secret variables (blank placeholders pass)
  "(UNIFI_[A-Z0-9_]*API_KEY|ADMIN_BOOTSTRAP_PASSWORD|SESSION_SECRET)[[:space:]]*[:=][[:space:]]*[\"']?[A-Za-z0-9_/+=.-]{12,}"
  # Hardcoded bearer tokens
  "Authorization:[[:space:]]*Bearer[[:space:]]+[A-Za-z0-9._-]{20,}"
  # Never disable TLS verification (ADR-003 failure mode #6)
  "InsecureSkipVerify[[:space:]]*:[[:space:]]*true"
  "curl.*[[:space:]](-k|--insecure)([[:space:]]|$)"
)

failed=0

# A committed .env (rather than .env.example) would leak secrets wholesale.
if git ls-files --error-unmatch .env >/dev/null 2>&1; then
  echo "BLOCKED: .env is tracked by git — remove it from the index and rotate every value it held."
  failed=1
fi

for pattern in "${patterns[@]}"; do
  # git grep scans tracked files only, so node_modules/dist are naturally excluded.
  if matches=$(git grep -n -I -E -e "$pattern" -- . ':(exclude)scripts/check-secrets.sh' 2>/dev/null); then
    echo "BLOCKED: pattern /$pattern/ matched:"
    echo "$matches" | sed 's/^/  /'
    failed=1
  fi
done

if [ "$failed" -ne 0 ]; then
  echo
  echo "Secret scan failed. Remove the value, rotate it, and re-commit."
  exit 1
fi

echo "Secret scan clean."

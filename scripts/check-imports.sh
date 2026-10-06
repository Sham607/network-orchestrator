#!/usr/bin/env bash
# Import-boundary guard.
#
# Enforces the one-way dependency rules from docs/adr/003-architecture.md §1 in
# CI, without depending on any external linter. Violations fail the build.
set -uo pipefail
cd "$(dirname "$0")/.."

base="github.com/Sham607/network-orchestrator/apps/api/internal"
failed=0

check_no_import() {
  local pkg="$1"; shift
  local imports
  imports=$(go list -f '{{join .Imports "\n"}}' "./apps/api/${pkg}" 2>/dev/null) || {
    echo "FAIL: cannot list imports of ${pkg}"
    failed=1
    return
  }
  local forbidden
  for forbidden in "$@"; do
    if grep -qx "$forbidden" <<<"$imports"; then
      echo "FAIL: ${pkg} must not import ${forbidden#${base}/}"
      failed=1
    fi
  done
}

# unifi is the leaf: it must never depend on the modules that consume it.
check_no_import "internal/unifi" "$base/api" "$base/inventory" "$base/identity" "$base/audit"
# api goes through inventory, never straight to the UniFi adapter.
check_no_import "internal/api" "$base/unifi"
# inventory depends on unifi + audit, never on the transport or identity.
check_no_import "internal/inventory" "$base/api" "$base/identity"

if [ "$failed" -ne 0 ]; then
  echo
  echo "Import boundary violated (see docs/adr/003-architecture.md §1)."
  exit 1
fi

echo "Import boundaries OK."

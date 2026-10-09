#!/usr/bin/env bash
# Contract-drift guard for the web client.
#
# apps/web/src/api/schema.d.ts is generated from openapi/openapi.yaml and
# committed, so the SPA has exactly one definition of every API type. This
# script regenerates it into a temp file and fails when the committed file no
# longer matches the contract — a shape change in openapi/openapi.yaml must come
# with a regenerated client in the same commit.
#
# Run locally with `make openapi-types`, or from apps/web with
# `npm run openapi:check`.
set -uo pipefail
cd "$(dirname "$0")/.."

target="apps/web/src/api/schema.d.ts"

if [ ! -f "$target" ]; then
  echo "FAIL: $target is missing; run 'cd apps/web && npm run openapi:gen'."
  exit 1
fi

generated="$(mktemp)"
trap 'rm -f "$generated"' EXIT

if ! (cd apps/web && npx --no-install openapi-typescript ../../openapi/openapi.yaml -o "$generated" >/dev/null); then
  echo "FAIL: openapi-typescript could not generate from openapi/openapi.yaml."
  echo "Install dependencies first: cd apps/web && npm ci"
  exit 1
fi

if ! diff -u "$target" "$generated"; then
  echo
  echo "FAIL: $target does not match openapi/openapi.yaml."
  echo "Regenerate and commit it: cd apps/web && npm run openapi:gen"
  exit 1
fi

echo "Generated web API types match openapi/openapi.yaml."

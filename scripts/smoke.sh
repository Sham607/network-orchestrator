#!/usr/bin/env bash
# Smoke test.
#
# Phase 1 (always, no secrets required): build the API, start it, and assert
#   the liveness endpoint returns 200 with a request-id header.
# Phase 2 (optional): if UNIFI_SM_API_KEY is set, run the adapter's live
#   read-only test (build tag `live`) against the real Site Manager API and, when
#   UNIFI_CONSOLE_ID is also set, the per-console Connector Proxy. This exercises
#   the shipped code path instead of a hand-rolled curl. CI never sets the key
#   and never builds with `-tags live`, so CI stays green without credentials.
#   Base URLs are overridable: UNIFI_SM_BASE_URL, UNIFI_CONSOLE_ID.
set -euo pipefail
cd "$(dirname "$0")/.."

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

addr=":18080"
base="http://127.0.0.1${addr}"

echo "== phase 1: local liveness =="
go build -o "$workdir/api" ./apps/api/cmd/api

DATABASE_URL="${SMOKE_DATABASE_URL:-postgres://smoke:smoke@127.0.0.1:5432/smoke?sslmode=disable}" \
UNIFI_ENABLED=false \
HTTP_ADDR="$addr" \
APP_ENV=test \
"$workdir/api" &
pid=$!
trap 'kill "$pid" 2>/dev/null || true; rm -rf "$workdir"' EXIT

ready=0
for _ in $(seq 1 50); do
  if curl -fsS -D "$workdir/headers" -o "$workdir/body" "${base}/api/v1/health" 2>/dev/null; then
    ready=1
    break
  fi
  sleep 0.2
done
[ "$ready" -eq 1 ] || { echo "FAIL: /api/v1/health never became ready"; exit 1; }

grep -q '"status":"ok"' "$workdir/body" || { echo "FAIL: unexpected health body: $(cat "$workdir/body")"; exit 1; }
grep -qi '^x-request-id:' "$workdir/headers" || { echo "FAIL: missing X-Request-Id header"; exit 1; }

# Liveness must not depend on the database, so /ready is expected to fail here
# (no Postgres in the smoke environment). Assert it fails the documented way —
# 503 + code service_unavailable — and not as a crash or a 500 (ADR-004).
code=$(curl -s -o "$workdir/ready" -w '%{http_code}' "${base}/api/v1/ready")
[ "$code" = "503" ] || { echo "FAIL: /ready returned $code, want 503 with no database"; exit 1; }
grep -q '"code":"service_unavailable"' "$workdir/ready" || { echo "FAIL: /ready body = $(cat "$workdir/ready"), want code service_unavailable"; exit 1; }

echo "OK: liveness 200 + request id; /ready returns 503 service_unavailable without a database."

echo "== phase 2: Site Manager read-only check =="
if [ -z "${UNIFI_SM_API_KEY:-}" ]; then
  echo "SKIP: UNIFI_SM_API_KEY not set (expected in CI)."
  exit 0
fi

sm_base="${UNIFI_SM_BASE_URL:-https://api.ui.com}"
UNIFI_SM_BASE_URL="$sm_base" go test -tags live -count=1 -run TestLiveReadOnly -v ./apps/api/internal/unifi/ || {
  echo "FAIL: live read-only check against $sm_base failed"
  exit 1
}
echo "OK: live read-only check passed."

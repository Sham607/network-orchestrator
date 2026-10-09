#!/usr/bin/env bash
# End-to-end harness runner (ZENI-16).
#
# Runs the integration suite in tests/e2e against a real Postgres cache and a
# mocked UniFi controller. It never needs a UniFi credential and never touches a
# live controller: the suite starts its own fixture server and strips any
# inherited UNIFI_* variables from the API process it launches.
#
#   Local:  make e2e            # starts Postgres via docker compose, then runs
#   CI:     E2E_DATABASE_URL=…  # use the Postgres service container instead
#
# Extra arguments are forwarded to `go test`, so `./scripts/e2e.sh -run TestX`
# runs a single test without re-implementing the bootstrap.
#
# With E2E_WEB=1 the Playwright web suite runs too (see scripts/e2e-web.sh); the
# browser dependencies are then installed on demand.
#
# E2E_DATABASE_URL must point at a DISPOSABLE database: the suite drops the
# schema and re-applies the migrations so every run starts from zero.
set -euo pipefail
cd "$(dirname "$0")/.."

compose_started=0
cleanup() {
  if [ "$compose_started" -eq 1 ]; then
    docker compose stop db >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

if [ "${E2E_WEB:-0}" = "1" ]; then
  if [ ! -d apps/web/node_modules ]; then
    echo "== installing web dependencies =="
    (cd apps/web && npm ci)
  fi
  if [ -z "${PLAYWRIGHT_CHROMIUM_PATH:-}" ]; then
    echo "== ensuring the Chromium build Playwright needs =="
    (cd apps/web && npx playwright install chromium)
  fi
fi

if [ -z "${E2E_DATABASE_URL:-}" ]; then
  # Only stop the database on exit if this run is what started it, so `make e2e`
  # never pulls the rug out from under a developer's own `make up`.
  if [ -z "$(docker compose ps -q db 2>/dev/null)" ]; then
    compose_started=1
  fi

  echo "== starting Postgres via docker compose =="
  docker compose up -d db

  echo "== waiting for Postgres =="
  ready=0
  for _ in $(seq 1 60); do
    if docker compose exec -T db pg_isready -U "${POSTGRES_USER:-orchestrator}" -d postgres >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done
  [ "$ready" -eq 1 ] || { echo "FAIL: Postgres never became ready"; exit 1; }

  # A dedicated, disposable database keeps the harness from wiping the dev cache.
  docker compose exec -T db psql -U "${POSTGRES_USER:-orchestrator}" -d postgres -v ON_ERROR_STOP=1 \
    -c "DROP DATABASE IF EXISTS orchestrator_e2e WITH (FORCE)" \
    -c "CREATE DATABASE orchestrator_e2e" >/dev/null

  export E2E_DATABASE_URL="postgres://${POSTGRES_USER:-orchestrator}:${POSTGRES_PASSWORD:-orchestrator}@127.0.0.1:5432/orchestrator_e2e?sslmode=disable"
fi

echo "== running the e2e suite =="
go test -tags e2e -count=1 -timeout 20m "$@" ./tests/e2e/...

echo "== e2e OK =="

#!/usr/bin/env bash
# Web E2E runner (ZENI-16): Playwright drives the dashboard in Chromium against
# the same stack as the Go harness — real API binary, disposable Postgres and a
# mocked UniFi controller. No UniFi credential, no live controller.
#
# The stack itself is owned by tests/e2e, so this script only turns the web test
# on and forwards everything to scripts/e2e.sh.
set -euo pipefail
cd "$(dirname "$0")/.."

export E2E_WEB=1
exec ./scripts/e2e.sh -run TestWebDashboardSlice

# E2E harness

The end-to-end layer of ZENI-16. It starts the **real API binary** against a
**disposable Postgres schema** and an **in-process mock of the UniFi surface**,
then exercises the product twice: once over HTTP (`tests/e2e`) and once through
the dashboard in a real browser (`apps/web/e2e`, Playwright).

Two properties are structural, not conventions:

- **It cannot reach UniFi.** Every stack is launched with a scrubbed
  environment (`cleanEnv` strips inherited `UNIFI_*`, `CLIENT_EVENTS_*`,
  `DATABASE_URL`, `HTTP_ADDR`, `APP_ENV`, `LOG_LEVEL`, `SYNC_*`) and, when UniFi
  is enabled, is pointed at a
  loopback `httptest` server. The mock also counts requests and can be driven
  offline or slow, so "the harness never touched UniFi" is asserted, not assumed.
- **It carries no credential.** The fixture credential is the 11-character
  string `fixture-key`; the secret scanner flags real-looking values, and this
  one must never be usable. `grep -rn UNIFI .github/workflows/` stays empty and
  neither CI job wires a secret.

The browser suite deliberately does **not** re-implement the stack: the Go
harness starts Postgres, the API and the mock, then starts the Vite dev server
with its `/api` proxy pointed at the API, and finally runs Playwright against
that origin. One set of fixtures, one database bootstrap, and the same mocked
controller for both layers.

## Running it

```bash
make e2e                 # API + integration suite: real Postgres, mocked UniFi
make e2e-web             # the Playwright dashboard suite on the same stack
```

Under the hood:

```bash
./scripts/e2e.sh                     # extra args go to `go test`
E2E_WEB=1 ./scripts/e2e.sh -run TestWebDashboardSlice
E2E_DATABASE_URL=… go test -tags e2e -count=1 ./tests/e2e/...
```

`E2E_DATABASE_URL` must point at a **disposable** database: the suite drops the
schema and re-applies `migrations/` so every run starts from zero. In CI both
jobs provide it through a Postgres service container.

`make e2e-web` needs `npm ci` and a Chromium build (it installs the latter on
demand). On a host where Playwright's bundled Chromium cannot run — musl/Alpine —
set `PLAYWRIGHT_CHROMIUM_PATH` to the system Chromium and the suite uses it
instead of skipping:

```bash
PLAYWRIGHT_CHROMIUM_PATH=/usr/bin/chromium make e2e-web
```

## Test matrix — API and integration

| Area | Case | Test |
|---|---|---|
| Vertical slice | sync fills the cache; `/sites`, `/devices`, `/ready`, `/sync/status` agree, every body validated against `openapi/openapi.yaml` | `TestVerticalSlice` |
| Query semantics | `status` filters (and is case-normalized); the undocumented `state` is not an alias | `TestStatusFilterUsesStatusNotState` |
| Pagination | `page_size`/`total`/`total_pages`; page past the data is an empty 200, not a 404 | `TestListPagination` |
| Validation & boundary | 13 invalid inputs (numeric bounds, junk enums, bad uuid/mac, over-long `q`) → `400 validation_failed` naming the offending field | `TestValidationErrors` |
| Routing | unknown path → 404 | `TestUnknownRouteIsNotFound` |
| Methods | `POST` on a read route → 405 | `TestMethodNotAllowed` |
| Auth boundary | endpoints are unauthenticated while FR-6 is deferred (pinned so the day auth lands it fails loudly) | `TestEndpointsAreUnauthenticatedInPhase1` |
| Race / concurrency | 24 parallel reads while the worker resyncs every second: no 5xx, no inconsistent totals | `TestConcurrentReadsWhileSyncing` |
| Cache boundary | a read is answered from Postgres, never calls the controller, and does not block on a slow controller | `TestReadsDoNotCallTheController` |
| Degradation · database down | `/ready` and the lists → `503 service_unavailable` (`details.reason: database_unreachable`); `/health` stays 200; `/sync/status` still answers | `TestReadyFailsWhenDatabaseUnreachable` |
| Degradation · controller down from boot | `/ready` and the lists → `503 unifi_unreachable` (`cache_stale`), never `service_unavailable` | `TestReadyFailsAsUnifiUnreachableWhenCacheStale` |
| Degradation · our own writes fail while the controller answers | `/ready` → `503 service_unavailable` (`cache_not_fresh` + `last_error_kind: internal_error`), never `unifi_unreachable` | `TestReadyBlamesOurOwnSideWhenOurWritesFail` (unit, `apps/api/internal/api`) |
| Degradation · stale cache before reachability catches up | `/ready` → `503 unifi_unreachable` (`cache_stale`) while `unifi.reachable` is still true and the last run succeeded | `TestReadyBlamesUnifiWhileReachabilityHasNotCaughtUp` (unit, `apps/api/internal/api`) |
| Degradation · healthy → offline | fresh 200 first; once the cache ages past `SYNC_STALE_AFTER`, the documented 503 appears and `/health` stays 200 | `TestControllerGoingOfflineMakesTheCacheStale` |
| Read-only · route table | the `chi` route inventory is exactly the five GET routes (unit test, `apps/api/internal/api`) | `TestRouterRegistersOnlyReadRoutes` |
| Read-only · over HTTP | the gated action routes reject writes, and no write verb is accepted on any route | `TestWriteActionRoutesRejectWrites`, `TestNoWriteMethodIsAccepted` |
| Retention (ADR-005) | with a 1h window and a 1s schedule, rows seeded after boot that are older than the window disappear, the newer row and `audit_log` stay, and the run logs `rows_deleted` | `TestClientEventsRetentionPrunesOnSchedule` |

## Test matrix — dashboard (Playwright, Chromium)

| Area | Case | Test |
|---|---|---|
| Vertical slice | `/` redirects to Sites, the site from the cache renders, navigating to Devices shows all three devices with their statuses, and the pager reflects the API's `pagination` | `the site and device views render what the API cache holds` |
| Boundary of trust | every request the page makes goes to the app's own origin — the browser never contacts the controller (acceptance criterion 5) | same test |
| Filtering | `status=OFFLINE` is applied by the API (the request carries it) and the URL keeps the filter; the unfiltered rows disappear | `the status filter is applied by the API, not in the browser` |
| Error path | a contract-shaped `503 unifi_unreachable` is surfaced with its code, HTTP status and `details.reason` | `a failing API is reported with its contract code, not a generic message` |
| Routing | an unknown path explains itself instead of rendering nothing | `an unknown path explains itself instead of rendering nothing` |

Flakiness is treated as failure: the only waits are condition polls
(`waitFor*`, Playwright auto-waiting assertions), never fixed sleeps, and
Playwright `retries` is 0.

## Proof the harness can fail (red → green)

A green suite is only evidence if it can go red for the right reason.

### API suite

The ADR-004 distinction was broken deliberately — one line in
`apps/api/internal/api/router.go`, the stale-cache readiness branch:

```diff
-  writeError(w, r, CodeUnifiUnreachable, "the UniFi cache is stale", details)
+  writeError(w, r, CodeServiceUnavailable, "the UniFi cache is stale", details)
```

`go test -tags e2e -v ./tests/e2e/...`:

```
--- FAIL: TestReadyFailsAsUnifiUnreachableWhenCacheStale (1.05s)
    degradation_test.go:66: /ready code = "service_unavailable", want unifi_unreachable (a dead database is service_unavailable, a dead controller is not)
--- FAIL: TestControllerGoingOfflineMakesTheCacheStale (10.44s)
    degradation_test.go:139: /ready code = "service_unavailable", want unifi_unreachable
...
FAIL	github.com/Sham607/network-orchestrator/tests/e2e	40.925s
```

The unit suite catches the same regression, so both jobs go red:

```
--- FAIL: TestReadyFailsWithUnifiUnreachableWhenCacheIsStale (0.03s)
    router_test.go:148: code = "service_unavailable", want "unifi_unreachable"
```

Reverting the line restores green with no other change: `ok … 26.946s`.

### Dashboard suite

The status filter was made a no-op in `apps/web/src/pages/DevicesPage.tsx` (the
`status` parameter was no longer sent to the API):

```
✘  2 [chromium] › e2e/dashboard.spec.ts:77:1 › the status filter is applied by the API, not in the browser (17.5s)
  Error: expect(locator).toHaveCount(expected) failed
  Locator:  getByRole('table', …).getByRole('row', { name: /AP Lobby/ })
  Expected: 0
  Received: 1
```

The browser really did render the unfiltered list. Reverting restores green:
`ok github.com/Sham607/network-orchestrator/tests/e2e 51.529s`.

## Known divergences (escalated, not silently fixed here)

None remaining:
1. Gated write routes (`/devices/{id}/actions` and `/clients/{id}/actions`) are registered with stubs returning `403 forbidden` and `details.reason: write_disabled` (resolved in ZENI-19).
2. Unrecognised query parameters on `/sites` and `/devices` are rejected with `400 validation_failed` naming the offending field (resolved in ZENI-19).

## Out of scope

Load/performance testing, and any test against a live controller — that stays
the deliberate manual read-only path in `scripts/smoke.sh`.

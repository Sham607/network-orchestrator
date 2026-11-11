# Network Orchestrator

Internal control plane for the office UniFi ecosystem: poll network state into
PostgreSQL, track client presence transitions and operational audit logs, and
expose everything to an operator NOC dashboard. Single-tenant, runs on one
Linux host in the office network.

**Status: Phase-1 Inventory Cache + Gated AP Maintenance & NOC Extensions.**
Agreed and tracked in the repository: the UniFi access path (ADR-001), stack
(ADR-002), module boundaries and credential model (ADR-003), readiness failure
semantics and attribution (ADR-004, ADR-007), `client_events` retention
(ADR-005), the phase-2 write gate policy (ADR-006), the initial database schema
(`migrations/0001_init.sql`), and the core REST contract
(`openapi/openapi.yaml`).

Implemented in code:
- Configuration loading, validation, and secret redaction (`internal/config`),
  structured JSON logging (`internal/observability`), and a single HTTP error
  envelope (`internal/api`).
- Dual-transport UniFi read adapter (`Site Manager Cloud` and `Console
  Integration API v1` / Connector Proxy) plus a Connector Proxy operational
  writer (`RestartDevice`, `TuneRadio`, `GetRogueAPs`) in `internal/unifi`.
- Background inventory sync worker with circuit breaker, `sync_runs` history,
  `client_events` connect/disconnect transition diffing, scheduled 30-day
  `client_events` retention pruner (`internal/inventory`), and append-only
  `audit_log` recorder (`internal/audit`).
- Operator authentication, Argon2id password verification, server-side
  `HttpOnly` session cookies, CSRF double-submit verification, and 3-tier RBAC
  (`viewer`, `operator`, `admin`) in `internal/identity` (`POST /auth/login`,
  `POST /auth/logout`, `GET /me`, gated via `AUTH_ENABLED=true`).
- Probe endpoints (`health`, `ready`, `sync/status`), cache-backed list and
  detail endpoints (`sites`, `sites/{id}`, `devices`, `devices/{id}`,
  `clients`, `clients/{id}`, `networks`, `networks/{id}`), site RF/WAN
  diagnostic endpoints (`sites/{id}/rf-scan`, `sites/{id}/wan-health`), a
  real-time WebSocket stream (`ws/telemetry` via `internal/telemetry`), and
  opt-in AP maintenance actions (`POST /devices/{id}/actions` behind
  `WRITE_ENABLED=true`).

Three consequences worth knowing before you point anything at this build:

- **Operator authentication is opt-in via `AUTH_ENABLED=true`.** By default
  (`AUTH_ENABLED=false`) local development and CI run unauthenticated; set
  `AUTH_ENABLED=true` along with `ADMIN_BOOTSTRAP_EMAIL` and
  `ADMIN_BOOTSTRAP_PASSWORD` in production so all dashboard reads and AP
  maintenance actions require a valid operator session and RBAC role.
- **Clients and VLAN networks only populate in console mode.** The Site Manager
  cloud surface (`UNIFI_MODE=cloud`, default) serves only `sites` and `devices`
  (ADR-001), so `GET /api/v1/clients` and `GET /api/v1/networks` return valid
  empty pages in `cloud` mode. Populate real client and network rows with
  `UNIFI_MODE=local`.
- **WAN health and telemetry derive from cached gateway/device state and raw
  counters.** `sites/{id}/wan-health` and `ws/telemetry` resolve gateway identity,
  online device ratios, 24h disconnect drops, and client uplink AP names from
  PostgreSQL cache (`internal/inventory/read.go`), falling back to baseline
  estimates only when the UniFi surface omits byte or latency telemetry.
---

## Prerequisites

| Tool | Version | Needed for |
|---|---|---|
| Go | 1.23+ | API binary, migrations, E2E harness |
| Node.js | 22+ | Web SPA & Vitest (`jsdom` 30 requires `^22.22.2 \|\| ^24.15.0 \|\| >=26.0.0`), OpenAPI lint |
| Docker + Compose | recent | Local PostgreSQL 16 & dev stack |
| `make` | any | Developer & CI-parity targets |

## Quick start

```bash
git clone https://github.com/Sham607/network-orchestrator.git
cd network-orchestrator

cp .env.example .env      # local values only; never commit .env
make bootstrap            # go mod download + npm ci

# Start Postgres and apply the schema BEFORE the API. "make up" does not run
# migrations, and the sync worker starts polling the moment the API is up: start
# it against an empty database and the first cycle fails on every site
# ("relation sites does not exist") and records a partial/failed run, which only
# clears on the next tick.
docker compose up -d db   # Postgres only (:5432)
make migrate              # runs goose migrations against local Postgres

make up                   # API (:8080) + Postgres, now against a live schema

# Expect: {"status":"ok",...} with HTTP 200
curl -s localhost:8080/api/v1/health
```

`make up` starts the API with UniFi disabled (`UNIFI_ENABLED=false`) and writes
disabled (`WRITE_ENABLED=false`), matching the baseline CI and smoke-test
configuration. The service boots with no UniFi credentials present.

`make down` stops the stack but **keeps the database volume**. To wipe the dev
database and start over, use `docker compose down -v`.

### Running each part directly

```bash
make test           # Go unit tests
make test-web       # Web component tests (Vitest)
make build          # API binary + web bundle
make e2e            # integration harness: real Postgres + mocked UniFi, no secrets
make e2e-web        # the same stack, driven through the dashboard by Playwright
make smoke          # build + start the API + assert liveness/readiness behaviour
make web            # Vite dev server on :5173, proxies /api (incl. WS) to :8080
make migrate        # apply pending database migrations
make migrate-status # show database migration status
make check          # fast inner-loop checks (lint, typecheck, both test suites,
                    # import boundaries, secret scan, OpenAPI lint, generated-type drift)
make verify         # full local CI parity (all checks that run without a database:
                    # check + build + smoke)
```

`make check` is a fast inner-loop check for active development. `make verify`
provides full parity with CI jobs that run without an external database (`lint`,
`typecheck`, `test`, `test-web`, `build`, `imports`, `secrets`, `openapi`,
`openapi-types`, `smoke`). CI additionally runs `make e2e` and `make e2e-web`
against a disposable Postgres 16 service.

`make help` lists all targets.

## Configuration & UniFi credentials

`apps/api/internal/config` is the sole reader of environment variables:

| Variable | Default | Validation / Purpose |
|---|---|---|
| `APP_ENV` | `development` | Runtime environment label |
| `HTTP_ADDR` | `:8080` | HTTP listen address (also read by `/api --healthcheck`) |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `DATABASE_URL` | *(required)* | Must be a valid `postgres://` or `postgresql://` DSN with host |
| `UNIFI_ENABLED` | `false` | Enables `inventory.Worker` background polling |
| `UNIFI_MODE` | `cloud` | `cloud` (Site Manager API) or `local` (Console Integration API v1) |
| `WRITE_ENABLED` | `false` | Gates `POST /api/v1/devices/{id}/actions` (`403 write_disabled` when `false`) |
| `UNIFI_SM_API_KEY` | `""` | Required when `UNIFI_ENABLED=true` and `UNIFI_MODE=cloud`; also initializes `unifi.Writer` for AP actions and `rf-scan` |
| `UNIFI_LOCAL_URL` | `""` | Required whenever `UNIFI_MODE=local` |
| `UNIFI_LOCAL_API_KEY` | `""` | Required when `UNIFI_ENABLED=true` and `UNIFI_MODE=local` |
| `UNIFI_CONSOLE_ID` | `""` | Optional; passed to the console adapter on the Connector Proxy path |
| `UNIFI_CA_FILE` | `""` | Optional PEM path to pin a controller CA (TLS verification is never disabled) |
| `SYNC_INTERVAL` | `60s` | Poll interval (positive Go duration) |
| `SYNC_STALE_AFTER` | `180s` | Cache staleness threshold; **must be strictly greater than `SYNC_INTERVAL`** |
| `SYNC_SITE_ALLOWLIST` | *(all sites)* | Optional comma-separated list of site IDs or site names to sync |
| `CLIENT_EVENTS_RETENTION` | `720h` | Retention window (default 30 days) for `client_events` rows (ADR-005) |
| `CLIENT_EVENTS_PRUNE_INTERVAL` | `1h` | Run interval for the `client_events` retention pruner |
| `ADMIN_BOOTSTRAP_PASSWORD` | `""` | Loaded and redacted for future initial admin bootstrap (not wired yet) |

The API key gives access to the production office network and is treated as a
zero-trust secret: it never enters git, a Docker image, the database, logs, an
issue, or the browser (see `docs/adr/003-architecture.md` §4).

**Local development**

1. Edit your gitignored `.env` (created from `.env.example`).
2. Set `UNIFI_ENABLED=true`, `UNIFI_MODE=cloud|local`, and the matching key/URL
   fields.
3. Restart the process. Startup **fails fast** if a required variable is missing
   or invalid — with an error that names the variable but never prints its
   value.

**On the office host**

- Put secrets in `/etc/network-orchestrator/network-orchestrator.env` with
  permissions `0600`, or inject via Docker secrets.
- Pass them to the container as runtime environment variables. Never bake them
  into an image or pass them on a command line.
- Rotate by updating the secret and restarting the container (`SIGHUP` is
  trapped so the process does not exit, but live credential reload is not wired
  yet).
- Keep the file out of code backups.

Verify credential wiring without leaking it: `make smoke` only calls the live
Site Manager API when `UNIFI_SM_API_KEY` is set in your environment, and its
`live` build-tag test (`apps/api/internal/unifi/live_test.go`) is strictly
read-only.

> **How the UniFi package is wired:** `apps/api/internal/unifi` exposes both
> read transports behind `unifi.Client` (`cloud` and `console`) and operational
> Connector Proxy calls behind `unifi.Writer` (`RestartDevice`, `TuneRadio`,
> `GetRogueAPs`). `apps/api/internal/inventory` polls `unifi.Client` into
> Postgres, records every cycle in `sync_runs`, and writes an `audit_log` entry
> per cycle. With `UNIFI_ENABLED=false` no sync worker runs, though the local
> `client_events` retention pruner and WebSocket telemetry engine still start.

## Repository layout

```
apps/api          Go modular monolith (single binary)
  cmd/api         entrypoint, --healthcheck probe, writer & telemetry wiring
  internal/
    api           HTTP transport (/api/v1), error model, probes, resource & action handlers
    config        env/secret loading, validation, redaction (the ONLY package reading env)
    observability structured JSON logging (slog), header redaction
    unifi         the ONLY package that talks to UniFi (Client: cloud + console reads;
                  Writer: AP restart, radio tuning & rogue AP scan via Connector Proxy)
    inventory     Postgres cache repository, sync worker + circuit breaker,
                  client_events transition tracking, scheduled retention pruner
    telemetry     WebSocket fan-out hub & 2.5s loss / traffic-spike broadcast engine
    identity      operators, sessions, RBAC (stub doc.go; code pending)
    audit         append-only action trail (audit_log Postgres recorder)
apps/web          React 18 + TypeScript + Vite NOC SPA
  src             pages, drawers/modals, WebSocket hook, OpenAPI-typed client, Vitest tests
  e2e             Playwright Chromium suite (orchestrated by tests/e2e)
docs              data-model.md, architecture decision records (adr/), E2E docs (testing/)
openapi           openapi.yaml contract + mock-expectations.md
migrations        forward-only goose SQL migrations (0001_init.sql)
scripts           import-boundary, secret scan, OpenAPI type-drift, smoke & E2E runners
tests/e2e         full-stack integration harness (real Postgres + mocked UniFi + Playwright)
```

Module dependencies flow one way: `api → inventory → unifi` (plus `audit` and
`telemetry`; `cmd/api/main.go` adapts `unifi.Writer` to `api.DeviceWriter` so
`internal/api` never imports `internal/unifi`). This boundary is enforced in CI
by `scripts/check-imports.sh`.

## API surface

`openapi/openapi.yaml` defines the phase-1 REST contract (`openapi/mock-expectations.md`
documents mock invariants), while the router additionally serves three runtime
NOC extensions (`rf-scan`, `wan-health`, `ws/telemetry`):

| Endpoint | In `openapi.yaml` | Purpose | State |
|---|---|---|---|
| `GET /api/v1/health` | Yes | Liveness. Always `200 OK` while the process runs; never touches DB/UniFi. | **Served** |
| `GET /api/v1/ready` | Yes | Readiness. `200 OK` once Postgres answers and (when `UNIFI_ENABLED=true`) cache is fresher than `SYNC_STALE_AFTER`; `503` otherwise. | **Served** |
| `GET /api/v1/sync/status` | Yes | Worker freshness, circuit breaker state, UniFi reachability/surface, and last run summary. | **Served** |
| `POST /api/v1/auth/login` · `POST /api/v1/auth/logout` · `GET /api/v1/me` | Yes | Operator session + role. | **Contract only** — plain `404` |
| `GET /api/v1/sites` | Yes | Sites from cache: paged (`page`, `page_size`), sorted (`name`, `synced_at`), searched (`q`). | **Served** |
| `GET /api/v1/sites/{id}` | Yes | Single site detail from cache (also includes `console_id`). | **Served** |
| `GET /api/v1/sites/{id}/rf-scan` | No | Queries neighboring rogue APs via `unifi.Writer` (when `UNIFI_SM_API_KEY` is set) and returns 2.4GHz/5GHz channel contention, cleanest Non-DFS channels, and top strong interferers. | **Served** |
| `GET /api/v1/sites/{id}/wan-health` | No | Verifies site in cache and returns WAN health summary (currently synthetic baseline values). | **Served** *(synthetic)* |
| `GET /api/v1/devices` · `GET /api/v1/devices/{id}` | Yes | APs, switches, gateways from cache (filter by `site_id`, `status`, `device_type`, `mac`; paged & sorted). Normalized `radios` is currently `[]` (raw payload stays internal in DB `raw`). | **Served** |
| `GET /api/v1/clients` · `GET /api/v1/clients/{id}` | Yes | Connected or historical clients (`site_id`, `connected`, `uplink_device_id`, `client_type`, `mac`, inert `ssid`). `ssid` and `signal_dbm` are `null` in Integration API v1. | **Served** |
| `GET /api/v1/networks` · `GET /api/v1/networks/{id}` | Yes | VLAN/subnet configuration (`site_id`, `vlan_id`, `management`, `enabled`; not WLAN/SSID). | **Served** |
| `GET /api/v1/ws/telemetry` | No | WebSocket upgrade streaming 2.5s WAN latency/loss and top-talker ticks/alerts (currently backed by cached site/client rows + synthetic counters). | **Served** *(synthetic)* |
| `POST /api/v1/devices/{id}/actions` | Yes (`proposed-gated`) | AP operational actions (`RESTART`, `TUNE_RADIO`). Rejects non-AP devices (`400 unsupported_device_type`), calls `unifi.Writer`, writes `audit_log`, returns `202 Accepted`. Returns `403` (`details.reason: write_disabled`) when `WRITE_ENABLED=false`. | **Served** (`WRITE_ENABLED=true`) |
| `POST /api/v1/clients/{id}/actions` | Yes (`proposed-gated`) | Client block/unblock actions. | **Gated off** — `403` (`write_disabled`) |

Every registered REST route answers an error with one JSON shape:
`{ "code", "message", "details" }`, where `code` is stable and machine-readable
and `details.request_id` echoes `X-Request-Id`. Routes marked *Contract only*
are not registered yet and fall through to `chi`'s plain-text `404 page not
found`, which the dashboard uses to distinguish "endpoint not implemented yet"
from a structured resource `not_found`.

### Readiness & cache-staleness semantics

- `/ready` checks PostgreSQL first (`2s` timeout) and fails with **`503
  service_unavailable`** (`details.reason: database_unreachable`) when the
  database is down (ADR-004) — never `500 internal_error`, which is reserved
  for unexpected bugs. `scripts/smoke.sh` asserts this behaviour in CI.
- With `UNIFI_ENABLED=true`, `/ready` also requires a cache fresher than
  `SYNC_STALE_AFTER` (ADR-003 §6). By default a stale cache is attributed to the
  controller (**`503 unifi_unreachable`**, `details.reason: cache_stale`). It
  shifts to **`503 service_unavailable`** (`details.reason: cache_not_fresh`,
  carrying `last_error_kind`) only when there is positive evidence our own side
  failed while the controller remained reachable (`status.Reachable &&
  status.LastErrorKind == "internal_error"`, ADR-007). With
  `UNIFI_ENABLED=false`, `/ready` stays a database-only probe.
- Cache read handlers (`sites`, `devices`, `clients`, `networks`) query Postgres
  first and, if `UNIFI_ENABLED=true` while the cache is stale **and** the
  controller is marked unreachable, refuse to serve stale data with `503
  unifi_unreachable` (`details.reason: cache_stale`). Otherwise they return
  `200 OK` with per-row `synced_at`.

## Web dashboard

`apps/web` is the operator NOC SPA (React 18 + TypeScript + Vite + Tailwind CSS
+ React Router + TanStack Query, ADR-002 §2). Five views are routed (`/`
redirects to `/sites`): `/sites`, `/devices`, `/clients`, `/networks`, and
`/topology`.

- **Enterprise NOC Shell (`App.tsx`)**: Left sidebar with live sync health,
  surface (`cloud` vs `console`), and circuit-breaker status (`GET
  /api/v1/sync/status`), light/dark NOC theme toggle, header `LiveTicker`
  sparklines (WAN RTT & packet loss over `/api/v1/ws/telemetry`), and a
  dismissible alert banner on packet-loss or client traffic spikes.
- **Sites & RF/WAN Diagnostics (`/sites`)**: Site inventory with per-site modals
  for **RF Spectrum Analyzer (`RFHeatmapModal`)** (2.4GHz/5GHz channel
  contention bars, cleanest Non-DFS recommendations, strongest rogue AP
  interferers) and **WAN Health Monitor (`WANHealthModal`)**.
- **Devices & AP Maintenance (`/devices`)**: Executive KPI stat cards,
  URL-synced server-side filters (`site_id`, `status`, pagination), interactive
  **`DeviceDrawer`** (specifications, raw payload viewer, AP **Reboot**, and
  **Radio Channel Tuning** on 5GHz HT40 Non-DFS Ch `36/44/149/157/161` or 2.4GHz
  HT20 Ch `1/6/11`), **`BatchOptimizerModal`** (sequential 5GHz HT40 AP channel
  optimization with a 15-second inter-AP safety countdown), and
  **`AuditReportModal`** (printable A4 light-theme report via browser print /
  save-as-PDF).
- **Clients (`/clients`)**: Real-time **`TopTalkersWidget`**, server-side
  filters (`site_id`, `connected`, `client_type`) + local page search (`q`),
  surface-aware empty-state guidance for `UNIFI_MODE=cloud`, and
  **`ClientDrawer`**.
- **Networks (`/networks`)**: VLAN/subnet table with validated `vlan_id`
  (`0..4094`), `management`, and `enabled` filters plus local page search (`q`).
- **Topology (`/topology`)**: Per-site visual `Gateway → Switch → Access Point`
  hierarchy with click-to-inspect `DeviceDrawer` integration.
- **Contract-driven types & network isolation**: Core types in
  `apps/web/src/api/schema.d.ts` are generated by `openapi-typescript` and
  checked for drift by `make openapi-types` (extended by `RFScanResponse` and
  `WANHealthResponse` in `src/api/types.ts`). Every browser request stays
  same-origin under `/api/v1`; Playwright E2E tests assert the browser never
  contacts external hosts.

Component tests (`apps/web/src/test`, Vitest + Testing Library) cover API client
error parsing and populated, empty, filtered, paged, and error (`404`/`503`)
states across `SitesPage`, `DevicesPage`, `ClientsPage`, and `NetworksPage`. Run
them with `make test-web`.

## CI

`.github/workflows/ci.yml` runs on every push to `main` and every PR:

| Job | Checks |
|---|---|
| `api` | `gofmt`, `go vet` (incl. `-tags e2e`), `go build`, `go test`, import boundaries (`scripts/check-imports.sh`) |
| `web` | Node 22: `npm ci`, ESLint, `tsc`, Vitest, Vite build, generated-type drift (`npm run openapi:check`) |
| `openapi` | Redocly lint of `openapi/openapi.yaml` |
| `secrets` | `scripts/check-secrets.sh` — high-confidence secrets, tracked `.env`, and TLS bypasses |
| `smoke` | Build the API, start it without Postgres, assert `/health` `200` (`X-Request-Id`) + `/ready` `503 service_unavailable` |
| `e2e` | Full Go integration harness against Postgres 16 + mocked UniFi, JSON-Schema contract-checked |
| `e2e-web` | Playwright drives the dashboard in Chromium against the same Go harness + Vite dev server |

No CI job needs a UniFi key. Real-network verification stays a deliberate,
manual, read-only step (`UNIFI_SM_API_KEY=... make smoke`).

### End-to-end harness

`tests/e2e` (`//go:build e2e`) compiles and starts the real API binary against a
disposable Postgres schema and an in-process mock of the UniFi Console
Integration API (`mockUnifi`), stripping any inherited `UNIFI_*` environment
variables so it can never reach a live controller. It verifies:
- Vertical inventory slices: `sync → cache → /sites, /devices, /clients, /networks`
  (every `200 OK` response validated against `openapi/openapi.yaml`).
- Client presence transitions (`connected` → `disconnected` → `connected`) in
  `clients` and `client_events`, plus scheduled `client_events` retention
  pruning (`prune_test.go`).
- Readiness and read-path degradation under dead database, dead controller, and
  live controller outage crossing `SYNC_STALE_AFTER` (`degradation_test.go`).
- Default write-gate enforcement (`403 write_disabled` on `POST .../actions` and
  rejection of mutating verbs on read routes in `readonly_test.go`).
- Browser E2E (`web_test.go` with `E2E_WEB=1` / `make e2e-web`): boots the Vite
  dev server proxied to the live Go harness and runs the Playwright suites in
  `apps/web/e2e` (`dashboard.spec.ts`, `clients.spec.ts`, `networks.spec.ts`).

See `docs/testing/e2e-harness.md` for harness details.

## Security rules

- Real secrets live only in the runtime environment of the host. `.env` is
  gitignored; only `.env.example` (blank secret placeholders) is tracked, and
  `scripts/check-secrets.sh` blocks tracked `.env` files or leaked keys.
- Never disable TLS verification (`InsecureSkipVerify` and `curl -k` fail CI).
  Pin a self-signed controller CA via `UNIFI_CA_FILE`.
- Logs are structured JSON and never emit raw headers or payloads;
  `config.Config.Redacted()` scrubs secrets and DSN passwords for diagnostics.
- Write actions default off (`WRITE_ENABLED=false`). Enabling
  `WRITE_ENABLED=true` activates `POST /api/v1/devices/{id}/actions` (`RESTART`
  and `TUNE_RADIO`), which enforces an AP-only hardware guardrail
  (`device_type == "ap"`) and writes an `audit_log` entry; `POST
  /api/v1/clients/{id}/actions` remains locked (`403 write_disabled`). See
  ADR-006 for the full production write-gate policy.
- Operator authentication, session cookies, RBAC, and CSRF (`internal/identity`)
  are **not implemented yet**: every `/api/v1` endpoint is unauthenticated on
  the wire. Treat any deployment as strictly office LAN/VPN-only and never
  publish it externally.

## Branch protection

**Not enforced.** `main` is protected by convention only: changes land through a
pull request with green CI. GitHub branch protection rules on a private
repository require a paid plan, and this repository is not on one — that was
verified, not assumed. Enforcing it for real means moving the repository to an
organisation with a Team plan or above.

Until then a direct push to `main` is possible and would bypass CI entirely.

## Decisions

Read `docs/adr/` before proposing structural changes. Accepted ADRs are
immutable — superseding a decision requires a new ADR:

- **ADR-001** (`001-unifi-api-surface.md`): UniFi API surfaces, `X-API-KEY` auth, and Cloud Connector Proxy verification.
- **ADR-002** (`002-tech-stack.md`): Tech stack (Go 1.23 + `chi` + `pgx`, Postgres 16, React 18 + Vite + Tailwind SPA).
- **ADR-003** (`003-architecture.md`): Modular-monolith boundaries, sync/cache model, credential handling, error shape, and failure modes.
- **ADR-004** (`004-readiness-failure-code.md`): Readiness failure uses `503 service_unavailable`, never `500 internal_error` (stale-cache clause refined by ADR-007).
- **ADR-005** (`005-client-events-retention.md`): 30-day `client_events` retention window and scheduled application-side pruning.
- **ADR-006** (`006-write-activation-gate.md`): Phase-2 write-operation activation prerequisites and explicit `403 write_disabled` gate.
- **ADR-007** (`007-readiness-attribution.md`): Readiness blames a stale cache on `unifi_unreachable` unless positive `internal_error` evidence proves our own side failed.

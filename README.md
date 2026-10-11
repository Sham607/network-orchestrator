# Network Orchestrator

Internal control plane for the office UniFi ecosystem: read network state,
cache it in Postgres, and expose it to an operator dashboard. Single-tenant,
runs on one Linux host in the office network.

**Status: phase 1, read path deployed — writes and operator auth are not.** In
the repository and
agreed: the stack (ADR-002), module boundaries and credential model (ADR-003),
the UniFi access path (ADR-001), the phase-1 schema (`migrations/0001_init.sql`)
and the phase-1 API contract (`openapi/openapi.yaml`).

Implemented in code: configuration loading and validation, structured logging,
the error model, the read-only UniFi adapter (both transports), the inventory
sync worker with its Postgres cache and audit trail, the three probe endpoints,
and the cache-backed read endpoints (`sites`, `devices`, `clients`). Not
implemented: operator auth, site/device detail, networks, and every real write.
The contract describes them; the code does not
serve them. See
[API surface](#api-surface) for exactly what responds today.

Two consequences worth knowing before you point anything at this build:

- **The cache read endpoints are unauthenticated** until operator auth lands.
  Keep the API on the office LAN or VPN; do not expose it to the internet.
- **A client list only has rows when the surface can read clients.** The cloud
  surface serves no clients at all (ADR-001), so with the default
  `UNIFI_MODE=cloud`, `GET /api/v1/clients` is a well-formed empty page. Real
  client rows need `UNIFI_MODE=local` on the office LAN/VPN.

---

## Prerequisites

| Tool | Version | Needed for |
|---|---|---|
| Go | 1.23+ | API |
| Node.js | 20+ | Web SPA, OpenAPI lint |
| Docker + Compose | recent | Dev stack |
| `make` | any | Convenience targets |

## Quick start

```bash
git clone https://github.com/Sham607/network-orchestrator.git
cd network-orchestrator

cp .env.example .env      # local values only; never commit .env
make bootstrap            # go mod download + npm ci

# Start Postgres and apply the schema BEFORE the API. "make up" does not run
# migrations, and the sync worker starts polling the moment the API is up: start
# it against an empty database and the first cycle fails on every site
# ("relation sites does not exist") and records a partial run, which only clears
# on the next tick.
docker compose up -d db   # Postgres only (:5432)
make migrate              # runs goose migrations against local Postgres

make up                   # API (:8080) + Postgres, now against a live schema

# Expect: {"status":"ok",...} with HTTP 200
curl -s localhost:8080/api/v1/health
```

`make up` starts the API with UniFi disabled (`UNIFI_ENABLED=false`), which is
the same configuration CI and the smoke test use. The service boots with no
UniFi credentials present.

`make down` stops the stack but **keeps the database volume**. To wipe the dev
database and start over, use `docker compose down -v`.

### Running each part directly

```bash
make test           # Go unit tests
make test-web       # Web component tests (Vitest)
make build          # API binary + web bundle
make e2e            # integration harness: real Postgres + a mocked UniFi, no secrets
make e2e-web        # the same stack, driven through the dashboard by Playwright
make smoke          # build + start the API + assert liveness/readiness behaviour
make web            # Vite dev server on :5173, proxies /api to :8080
make migrate        # apply pending database migrations
make migrate-status # show database migration status
make check          # fast inner-loop checks (lint, typecheck, both test suites,
                    # import boundaries, secret scan, OpenAPI lint, generated-type drift)
make verify         # full local CI parity (all checks that run without a database:
                    # check + build + smoke)
```

`make check` is a fast inner-loop check for active development. `make verify` provides
full parity with CI jobs that run without an external database (`lint`, `typecheck`,
`test`, `test-web`, `build`, `imports`, `secrets`, `openapi`, `openapi-types`, `smoke`).
CI additionally runs `make e2e` and `make e2e-web` which require a disposable Postgres service.

`make help` lists all targets.

## Injecting UniFi credentials

The API key gives access to the production office network and is treated as a
zero-trust secret: it never enters git, a Docker image, the database, logs, an
issue, or the browser (see `docs/adr/003-architecture.md` §4).

**Local development**

1. Edit your gitignored `.env` (created from `.env.example`).
2. Set `UNIFI_ENABLED=true`, `UNIFI_MODE=cloud|local`, and the key fields.
3. Restart the process. Startup **fails fast** if the mode is enabled and its
   credentials are missing — with an error that names the variable but never
   prints its value.

**On the office host**

- Put the secret in `/etc/network-orchestrator/network-orchestrator.env` with
  permissions `0600`, or a Docker secret.
- Pass it to the container as a runtime environment variable. Never bake it into
  an image, never pass it on a command line.
- Rotate by updating the secret and restarting the container (or `SIGHUP`).
- Keep the file out of code backups.

Verify credential wiring without leaking it: `make smoke` only calls the live
Site Manager API when `UNIFI_SM_API_KEY` is set in your environment, and it is
read-only.

> **The adapter is wired into the API through the sync worker.**
> `apps/api/internal/unifi` exposes both read-only transports behind the
> `unifi.Client` interface (Site Manager cloud and per-console Connector
> Proxy); `apps/api/internal/inventory` polls it into Postgres, records every
> cycle in `sync_runs` and writes an audit entry per cycle. With
> `UNIFI_ENABLED=false` no worker runs and no request is made.
> Note also that `UNIFI_MODE=local` requires `UNIFI_LOCAL_URL` and
> `UNIFI_CONSOLE_ID`, so leaving the default `cloud` mode is easier for local
> work.

## Repository layout

```
apps/api          Go modular monolith (single binary)
  cmd/api         entrypoint
  internal/
    api           HTTP transport, error model, health endpoints
    config        env/secret reading + validation (the only place that reads env)
    observability structured logging, redaction
    unifi         the ONLY package that talks to UniFi (read-only client:
                  cloud + console transports, interface + domain types)
    inventory     local cache + sync worker        (sites/devices/clients/networks,
                  sync_runs, circuit breaker)
    identity      operators, sessions, RBAC        (code pending)
    audit         append-only action trail         (audit_log writer)
apps/web          React + TypeScript + Vite SPA
  e2e             Playwright dashboard suite (run by tests/e2e)
docs/adr          architecture decision records
openapi           the backend ↔ dashboard contract
migrations        forward-only goose migrations
scripts           import-boundary, secret scan, smoke test, e2e runners
tests/e2e         integration harness (real Postgres + mocked UniFi)
```

Dependencies flow one way: `api → inventory → unifi`, plus `audit`. The rule is
enforced by `scripts/check-imports.sh` in CI, not by convention.

## API surface

`openapi/openapi.yaml` is the phase-1 contract: probes, operator auth, and the
cache-backed `sites` / `devices` / `clients` / `networks` list and detail
endpoints, plus two **gated** write operations that are disabled until the UniFi
write scope is approved. Resource handlers land incrementally; the contract and
its mock expectations are complete today so the dashboard can be built in
parallel (`openapi/mock-expectations.md`).

| Endpoint | Purpose | State |
|---|---|---|
| `GET /api/v1/health` | Liveness. Always 200 while the process runs; never touches DB/UniFi. | **Served** |
| `GET /api/v1/ready` | Readiness. 200 once the database answers and, with UniFi enabled, the cache is fresher than `SYNC_STALE_AFTER`; `503 service_unavailable` / `503 unifi_unreachable` otherwise. | **Served** |
| `GET /api/v1/sync/status` | Cache freshness for operators, read from the live worker. | **Served** |
| `POST /api/v1/auth/login` · `POST /api/v1/auth/logout` · `GET /api/v1/me` | Operator session + role. | Contract only — returns 404 |
| `GET /api/v1/sites` | Sites from the cache: paged (`page`, `page_size`), sorted (`sort`, `order`), name search (`q`). | **Served** |
| `GET /api/v1/sites/{id}` | One site. | Contract only — returns 404 |
| `GET /api/v1/devices` | APs, switches, gateways from the cache (filter by site/status/type/mac), paged and sorted. | **Served** |
| `GET /api/v1/devices/{id}` | One device. | Contract only — returns 404 |
| `GET /api/v1/clients` · `/clients/{id}` | Clients, connected or historical. | **Served** |
| `GET /api/v1/networks` · `/networks/{id}` | VLAN/subnet config (not a WLAN). | Contract only — returns 404 |
| `POST /api/v1/devices/{id}/actions` · `POST /api/v1/clients/{id}/actions` | **Gated off** in phase 1; `403 write_disabled`. | **Served** as a stub: rejects before reading the body and never touches UniFi |

Every registered route answers an error with one shape: `{ "code", "message",
"details" }`, where `code` is stable and machine-readable. A route that is not
registered yet — every row marked *contract only* — falls through to the
router's plain `404 page not found`, which is deliberately **not** the contract
shape: the dashboard uses exactly that difference to say "not served yet"
instead of inventing an error it did not receive. See `openapi/openapi.yaml`.

### Readiness failure semantics

`/ready` fails with **`503 service_unavailable`** and
`details.reason: database_unreachable` when the database is unreachable (ADR-004).
A readiness failure is deliberately **not** `internal_error` — that code is
reserved for unexpected bugs in our own code, and conflating the two makes
alerting unable to tell "Postgres is restarting" from "we shipped a bug".
`scripts/smoke.sh` asserts exactly `503` + `service_unavailable`, so a
regression to `500` now fails CI.

With `UNIFI_ENABLED=true`, readiness additionally requires a cache fresher than
`SYNC_STALE_AFTER` (ADR-003 §6). A stale cache fails with **`503
unifi_unreachable`** and `details.reason: cache_stale`: the controller is the
dependency that is broken, so naming it is what tells an operator whether to
look at Postgres or at UniFi. The database is checked first, so a simultaneous
outage is always reported as `service_unavailable`. With `UNIFI_ENABLED=false`
there is no cache to be stale and the probe stays a database check.

## Web dashboard

`apps/web` is the operator SPA (React + TypeScript + Vite, ADR-002 §2). Three
views are implemented: sites (`/sites`), devices (`/devices`) and clients
(`/clients`), all built against `openapi/openapi.yaml`.

- **Types come from the contract.** `apps/web/src/api/schema.d.ts` is generated
  by `openapi-typescript` and committed; `npm run openapi:check` (and `make
  openapi-types` in CI) fails when it drifts from the contract. `src/api/types.ts`
  only aliases the generated types, so no API type is maintained twice.
- **Honest states.** `/sites`, `/devices` and `/clients` are all served from the
  local cache today (ZENI-15; `/clients` landed with the sync worker in ZENI-23).
  The views render the API's `{ code, message, details }` verbatim — `request_id`
  included — and say plainly when a 404 means "contract-only, not served yet", so
  the UI stays truthful against a build that does not serve them. No fabricated
  rows, no endless spinner.
- **Filtering is server-side.** The devices view sends `site_id`, `status`,
  `page` and `page_size`; the clients view sends `site_id`, `connected` and
  `client_type` the same way. The browser never downloads a table to filter it
  itself — with one deliberate exception: the clients view's free-text search
  box, because the contract exposes no hostname/IP filter for clients (only an
  exact `mac` match). It narrows only the rows already loaded for the current
  page, and the UI says so rather than implying a full-dataset search.
- **Read-only.** There is no write UI. The phase-1 action endpoints carry
  `x-status: proposed-gated` in the contract and are not registered by the API at
  all yet, so they answer 404 today; the contract documents `403 forbidden` with
  `details.reason: write_disabled` for the state where they are enabled. Nothing
  under `apps/web` can reach either one.
- **The browser never touches UniFi.** Every call goes to our own `/api/v1`
  (same-origin; Vite proxies it to `:8080` in dev), and no UniFi credential or
  controller URL appears anywhere under `apps/web`.

Tests are component-level (Vitest + Testing Library) and cover the three states
that matter: populated list, empty list, and API error (404 and 503). Run them
with `make test-web`.

## CI

`.github/workflows/ci.yml` runs on every push to `main` and every PR:

| Job | Checks |
|---|---|
| `api` | `gofmt`, `go vet`, `go build`, `go test`, import boundaries |
| `web` | `npm ci`, eslint, `tsc`, Vitest, Vite build, generated-type drift against `openapi/openapi.yaml` |
| `openapi` | Redocly lint of `openapi/openapi.yaml` |
| `secrets` | `scripts/check-secrets.sh` — high-confidence secrets and TLS bypasses |
| `smoke` | build the API, start it, assert liveness + clean readiness degradation |
| `e2e` | full integration harness: real Postgres, mocked UniFi, contract-checked bodies |
| `e2e-web` | Playwright drives the dashboard in Chromium against the same mocked stack |

No CI job needs a UniFi key. Real-network verification stays a deliberate,
manual, read-only step.

### End-to-end harness

`tests/e2e` (`//go:build e2e`) starts the real API binary against a disposable
Postgres schema and an in-process mock of the UniFi surface, then asserts the
whole vertical slice: sync → cache → `/sites` → `/devices`, plus readiness
degradation and the read-only boundary. It carries no credentials and can never
reach a live controller — the harness starts its own fixture server and strips
inherited `UNIFI_*` variables from the API process it launches, which is why
neither `e2e` job needs a secret.

The dashboard is driven over the same stack: the harness starts the Vite dev
server with its `/api` proxy pointed at the API and runs the Playwright suite in
`apps/web/e2e`, so there is one set of fixtures and no second database
bootstrap. `make e2e` and `make e2e-web` run both locally against the compose
database; see `docs/testing/e2e-harness.md` for the full test matrix and the
red→green evidence.

## Security rules

- Real secrets live only in the runtime environment of the host. `.env` is
  gitignored; only `.env.example` (no values) is tracked.
- Never disable TLS verification. Pin the controller CA via `UNIFI_CA_FILE`
  instead of reaching for `curl -k` or `InsecureSkipVerify`.
- Logs are structured and never receive raw headers or payloads; `config.Config`
  has a `Redacted()` view for diagnostics.
- UniFi write operations are out of scope for phase 1 and, when added, need an
  explicit `admin` role, CSRF token, rate limit, and an audit entry.
- The cache read endpoints (`sites`, `devices`, `clients`) carry **no
  authentication** until operator auth is implemented: nothing checks a session
  or a role. Treat any deployment as LAN/VPN-only and never publish it. This is
  a known phase-1 gap, not a configuration mistake.

## Branch protection

**Not enforced.** `main` is protected by convention only: changes land through a
pull request with green CI. GitHub branch protection rules on a private
repository require a paid plan, and this repository is not on one — that was
verified, not assumed. Enforcing it for real means moving the repository to an
organisation with a Team plan or above.

Until then a direct push to `main` is possible and would bypass CI entirely.

## Decisions

Read `docs/adr/` before proposing structural changes. ADR-001 fixes the UniFi
access path and auth (Connector Proxy, read-only in phase 1), ADR-002 fixes the
stack, ADR-003 fixes the module boundaries, credential handling and failure
modes, ADR-004 fixes the readiness failure code. A change to any of them
requires a new ADR — they are immutable once accepted.

# Network Orchestrator

Internal control plane for the office UniFi ecosystem: read network state,
cache it in Postgres, and expose it to an operator dashboard. Single-tenant,
runs on one Linux host in the office network.

**Status: foundation decided, product not implemented.** In the repository and
agreed: the stack (ADR-002), module boundaries and credential model (ADR-003),
the UniFi access path (ADR-001), the phase-1 schema (`migrations/0001_init.sql`)
and the phase-1 API contract (`openapi/openapi.yaml`).

Implemented in code: configuration loading and validation, structured logging,
the error model, the read-only UniFi adapter (both transports), the inventory
sync worker with its Postgres cache and audit trail, and **three probe
endpoints**. Not implemented: operator auth and every resource endpoint. The
contract describes them; the code does not
serve them. See
[API surface](#api-surface) for exactly what responds today.

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

make up                   # docker compose: API (:8080) + Postgres (:5432)

# Apply the schema. "make up" does NOT run migrations — without this step the
# database stays empty and goose_db_version never appears.
export DATABASE_URL='postgres://orchestrator:orchestrator@localhost:5432/orchestrator?sslmode=disable'
go run github.com/pressly/goose/v3/cmd/goose@latest \
  -dir ./migrations postgres "$DATABASE_URL" up

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
make test      # Go unit tests
make build     # API binary + web bundle
make smoke     # build + start the API + assert liveness/readiness behaviour
make web       # Vite dev server on :5173, proxies /api to :8080
make check     # lint, test, import boundaries, secret scan, OpenAPI lint
```

`make check` does **not** cover everything CI runs — CI additionally runs
`make build` and `make smoke`. A green `make check` is not a green CI. Run all
three before pushing.

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
docs/adr          architecture decision records
openapi           the backend ↔ dashboard contract
migrations        forward-only goose migrations
scripts           import-boundary, secret scan, smoke test
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
| `GET /api/v1/clients` · `/clients/{id}` | Clients, connected or historical. | Contract only — returns 404 |
| `GET /api/v1/networks` · `/networks/{id}` | VLAN/subnet config (not a WLAN). | Contract only — returns 404 |
| `POST /api/v1/devices/{id}/actions` · `POST /api/v1/clients/{id}/actions` | **Gated off** in phase 1; `403 write_disabled`. | Contract only — returns 404 |

Every error uses one shape: `{ "code", "message", "details" }`, where `code` is
stable and machine-readable. See `openapi/openapi.yaml`.

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

## CI

`.github/workflows/ci.yml` runs on every push to `main` and every PR:

| Job | Checks |
|---|---|
| `api` | `gofmt`, `go vet`, `go build`, `go test`, import boundaries |
| `web` | `npm ci`, eslint, `tsc` + Vite build |
| `openapi` | Redocly lint of `openapi/openapi.yaml` |
| `secrets` | `scripts/check-secrets.sh` — high-confidence secrets and TLS bypasses |
| `smoke` | build the API, start it, assert liveness + clean readiness degradation |

No CI job needs a UniFi key. Real-network verification stays a deliberate,
manual, read-only step.

## Security rules

- Real secrets live only in the runtime environment of the host. `.env` is
  gitignored; only `.env.example` (no values) is tracked.
- Never disable TLS verification. Pin the controller CA via `UNIFI_CA_FILE`
  instead of reaching for `curl -k` or `InsecureSkipVerify`.
- Logs are structured and never receive raw headers or payloads; `config.Config`
  has a `Redacted()` view for diagnostics.
- UniFi write operations are out of scope for phase 1 and, when added, need an
  explicit `admin` role, CSRF token, rate limit, and an audit entry.

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

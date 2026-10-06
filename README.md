# Network Orchestrator

Internal control plane for the office UniFi ecosystem: read network state,
cache it in Postgres, and expose it to an operator dashboard. Single-tenant,
runs on one Linux host in the office network.

**Status: bootstrap.** The repository skeleton, CI, environment wiring,
container stack and health checks are in place. Feature work (UniFi adapter,
domain schema, OpenAPI contract, dashboard) is tracked in the foundation epic.

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
curl -s localhost:8080/api/v1/health
```

`make up` starts the API with UniFi disabled (`UNIFI_ENABLED=false`), which is
the same configuration CI and the smoke test use. The service boots with no
UniFi credentials present.

### Running each part directly

```bash
make smoke     # build + start the API + assert liveness/readiness behaviour
make test      # Go unit tests
make web       # Vite dev server on :5173, proxies /api to :8080
make check     # everything CI runs
```

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

## Repository layout

```
apps/api          Go modular monolith (single binary)
  cmd/api         entrypoint
  internal/
    api           HTTP transport, error model, health endpoints
    config        env/secret reading + validation (the only place that reads env)
    observability structured logging, redaction
    unifi         the ONLY package that talks to UniFi (interface + domain types)
    inventory     local cache + sync worker        (ZENI-5)
    identity      operators, sessions, RBAC        (ZENI-6)
    audit         append-only action trail         (ZENI-5)
apps/web          React + TypeScript + Vite SPA
docs/adr          architecture decision records
openapi           the backend ↔ dashboard contract
migrations        forward-only goose migrations   (ZENI-5)
scripts           import-boundary, secret scan, smoke test
```

Dependencies flow one way: `api → inventory → unifi`, plus `audit`. The rule is
enforced by `scripts/check-imports.sh` in CI, not by convention.

## API surface

`openapi/openapi.yaml` is the phase-1 contract: probes, operator auth, and the
cache-backed `sites` / `devices` / `clients` / `networks` list and detail
endpoints, plus two **gated** write operations that are disabled until the UniFi
write scope is approved. Resource handlers land in follow-up work; the contract
and its mock expectations are complete today so the dashboard can be built in
parallel (`openapi/mock-expectations.md`).

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/health` | Liveness. Always 200 while the process runs; never touches DB/UniFi. |
| `GET /api/v1/ready` | Readiness. 503 when the database is unreachable or the cache is stale. |
| `GET /api/v1/sync/status` | Cache freshness for operators. |
| `POST /api/v1/auth/login` · `POST /api/v1/auth/logout` · `GET /api/v1/me` | Operator session + role. |
| `GET /api/v1/sites` · `/sites/{id}` | Sites. |
| `GET /api/v1/devices` · `/devices/{id}` | APs, switches, gateways (filter by site/status/type/mac). |
| `GET /api/v1/clients` · `/clients/{id}` | Clients, connected or historical. |
| `GET /api/v1/networks` · `/networks/{id}` | VLAN/subnet config (not a WLAN). |
| `POST /api/v1/devices/{id}/actions` · `POST /api/v1/clients/{id}/actions` | **Gated off** in phase 1; `403 write_disabled`. |

Every error uses one shape: `{ "code", "message", "details" }`, where `code` is
stable and machine-readable. See `openapi/openapi.yaml`.

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

Recommended settings for `main` (configure in GitHub → Settings → Branches):
require a pull request before merging, require the CI jobs to pass, and disallow
force pushes and branch deletion. The bootstrap commit predates the protection
rule; every later change should land through a PR.

## Decisions

Read `docs/adr/` before proposing structural changes. ADR-002 fixes the stack,
ADR-003 fixes the module boundaries, credential handling and failure modes. A
change to either requires a new ADR.

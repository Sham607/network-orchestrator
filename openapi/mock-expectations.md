# Mock expectations — running the dashboard without the backend

The OpenAPI contract (`openapi/openapi.yaml`) is the backend ↔ dashboard
boundary. This document says how the frontend consumes it **in parallel**, before
any resource handler exists, and which behaviours are part of the contract
rather than an implementation detail.

Owner: ZENI-6. Source of truth for shapes: `openapi/openapi.yaml`.

## 1. Generate the typed client

The contract is the single source for TypeScript types — do not hand-write
resource types (docs/adr/002-tech-stack.md §1, "Consequences").

```bash
npx openapi-typescript openapi/openapi.yaml -o apps/web/src/api/schema.d.ts
```

`openapi-typescript` is the intended codegen; wire it into `apps/web` when the
first data hook lands. Until then the frontend consumes the mock server below.

## 2. Run a mock server from the spec

```bash
npx --yes @stoplight/prism-cli@5 mock openapi/openapi.yaml --port 4010 --errors
# Prism serves the paths as written, at the root: http://localhost:4010/sites
```

Prism serves the named `examples` embedded in every operation, so every response
shape (including error cases) is already available. Note that Prism strips the
`/api/v1` server prefix (it serves `/sites`, not `/api/v1/sites`), so point the
Vite dev proxy at it with a rewrite:

```ts
// vite.config.ts (dev only)
server: {
  proxy: {
    '/api/v1': {
      target: 'http://localhost:4010',
      rewrite: (p: string) => p.replace(/^\/api\/v1/, ''),
    },
  },
}
```

Prism enforces the session-cookie security scheme, so protected routes return
`401` unless a cookie is sent. Add `Cookie: session=mock` to the proxy (or your
HTTP client) to exercise the success examples. Simulate an error branch with
`Prefer: code=429`; the gated write endpoints' `403 write_disabled` example is
reached with `Prefer: code=403` (Prism serves the success example by default,
since the gate is an application rule, not a spec rule).

Alternatively, hand-write MSW handlers from the same spec (`msw`), which is the
better fit for tests and for stateful login/logout. Either way the shapes must
mirror the spec examples.

## 3. Mock conventions the frontend may rely on

| Concern | Mock expectation |
|---|---|
| Base URL | `/api/v1`, same-origin in production. Under Prism the paths are served at the mock root (`http://localhost:4010/sites`); see the proxy rewrite in §2. |
| Auth | `POST /auth/login` returns `{ user }` and sets the `session` cookie. `GET /me` returns the same user. The SPA treats a 401 from any endpoint as "session gone" and routes to login. Prism needs `Cookie: session=mock` on protected calls. |
| Login mock | Prism does not persist cookies. In MSW, keep an in-memory flag: login → `200` + `user`, logout → `204`, `/me` → `401` when logged out. |
| Lists | Always `{ data, pagination }`. Never a bare array. `pagination.total_pages` is `0` for an empty set. |
| Empty state | Every list example includes an `empty` case (`data: []`). Build the empty UI now. |
| Errors | Prism selects error examples with `Prefer: code=429` (or `Prefer: code=403`). MSW: return the same `{ code, message, details }` body with the mapped HTTP status. |
| Rate limit | `429 rate_limited` carries `Retry-After` (seconds). The UI shows "try again in Ns" for login, and a soft notice for reads. |
| Stale data | `synced_at` is on every cache resource; `GET /sync/status` reports `stale` and `last_successful_sync_at`. The dashboard must render a global "data as of …" indicator from `/sync/status`, and per-row `synced_at` where it helps. |
| Never-null assumption | `clients.ssid` and `clients.signal_dbm` are **always null in phase 1** (Integration API v1 does not expose them). Render "unknown", never a fabricated value. `devices.uptime_sec` is null for the same reason. |
| Unknown enumerations | `Device.status` is an open string set (`ONLINE`, `OFFLINE`, `ADOPTING`, …). Treat unrecognized values as a neutral "unknown" badge, not an error. |
| Write endpoints | `POST /devices/{id}/actions` and `POST /clients/{id}/actions` are **gated off** in phase 1 and answer `403 forbidden` with `details.reason = "write_disabled"`. Mock them as disabled; do not build enabled UI paths until the write scope is approved. |
| Pagination size | Default `page_size=50`, max `200`. The UI must drive server-side pagination/filtering rather than fetching everything. |
| Sorting | `sort=<field>&order=asc|desc`; allowed fields are enumerated per endpoint. Wire the table header sorting to these params. |

## 4. Contract obligations the frontend must implement regardless of backend

These are contract-level, not deferred decisions:

1. **One error shape.** Branch on `code`, never on HTTP status or the message
   text. `details.request_id` may be shown in a "copy debug info" affordance.
2. **Always cache-backed.** No screen may assume data is live. Show freshness
   from `/sync/status`.
3. **Server-side list params.** Filter/paginate/sort via query params (matching
   the spec), so the same components work when the dataset grows.
4. **Nullable placeholders stay null.** SSID/signal/uptime must degrade to
   "unknown", not to invented values.

## 5. What is deliberately absent

- No `GET /wifi-broadcasts` (SSID/WLAN): Integration API v1 returns 404 for
  `/wifi` (docs/adr/001-unifi-api.md); the resource is deferred to a new ADR
  (docs/data-model.md §5). The `?ssid=` filter on `/clients` is published but
  currently inert for the same reason.
- No `GET /devices/{id}/stats` or other telemetry: no verified v1 surface, and
  `device_stats` is a deferred migration.
- No `/consoles` or host/backup endpoints: fleet health is exposed via
  `/sync/status` and `sites` until that resource has a data source.

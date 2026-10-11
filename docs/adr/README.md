# Architecture Decision Records

One file per decision, numbered, immutable once accepted except for the status
line. A decision that changes gets a new ADR that supersedes the old one.

| ADR | Title | Status |
|---|---|---|
| [001](001-unifi-api-surface.md) | UniFi API surface & auth validation | Accepted |
| [002](002-tech-stack.md) | Tech stack decision (BE/FE/DB/realtime/deploy/auth) | Accepted |
| [003](003-architecture.md) | System architecture & UniFi integration boundary | Accepted |
| [004](004-readiness-failure-code.md) | Readiness failure code (`service_unavailable` / 503) | Accepted (klausa "cache basi ⇒ `unifi_unreachable`" digantikan [ADR-007](007-readiness-attribution.md)) |
| [005](005-client-events-retention.md) | Retensi `client_events` (30 hari + pemangkasan terjadwal) | Accepted |
| [006](006-write-activation-gate.md) | Gerbang aktivasi operasi tulis fase 2 | Accepted |
| [007](007-readiness-attribution.md) | Atribusi readiness: bergeser hanya kalau ada bukti kegagalan sisi kita | Accepted |

## How code maps to these decisions

| Decision | Enforced by |
|---|---|
| One-way module dependencies (ADR-003 §1) | `scripts/check-imports.sh`, run in CI |
| Credentials never in git/log/DB (ADR-003 §4) | `.gitignore`, `scripts/check-secrets.sh`, `config.Redacted()`, logger redaction |
| Single error shape `{code, message, details}` (ADR-003 §5) | `apps/api/internal/api/errors.go`, `openapi/openapi.yaml` |
| Liveness/readiness split (ADR-003 §6) | `/api/v1/health` vs `/api/v1/ready` |
| Readiness failure is `503 service_unavailable` (ADR-004) | `apps/api/internal/api/errors.go`, `scripts/smoke.sh` |
| Readiness hanya menyebut dependensi yang benar-benar rusak berdasarkan bukti (ADR-007) | `handleReady` di `apps/api/internal/api/router.go`, `inventory.ErrKindInternal`, dipaku `apps/api/internal/api/router_test.go` dan `tests/e2e/degradation_test.go` |
| Fase 1 read-only terhadap UniFi (ADR-006) | hanya route `GET` yang didaftarkan di `apps/api/internal/api/router.go`; dijaga `apps/api/internal/api/routes_test.go` dan `tests/e2e/readonly_test.go` |
| Riwayat `client_events` dibatasi 30 hari (ADR-005) | **belum ada** — fitur presence klien (FR-3) belum dibangun, jadi belum ada baris yang dipangkas. Job pemangkasannya menyusul bersama FR-3. |
| Go 1.23 + chi + pgx, Postgres 16 (ADR-002) | `go.mod`, `docker-compose.yml` |
| React + Vite SPA (ADR-002) | `apps/web` |
| UniFi access lewat Connector Proxy cloud (ADR-001) | `apps/api/internal/unifi`, `UNIFI_SM_API_KEY` |

## Adding an ADR

Copy the structure of an existing file: context → decision → consequences →
rejected alternatives. Add it to the table above.

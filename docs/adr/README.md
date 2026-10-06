# Architecture Decision Records

One file per decision, numbered, immutable once accepted except for the status
line. A decision that changes gets a new ADR that supersedes the old one.

| ADR | Title | Status |
|---|---|---|
| 001 | UniFi API surface & auth validation | Pending — owned by ZENI-2 (blocked on controller access; see that issue's comments) |
| [002](002-tech-stack.md) | Tech stack decision (BE/FE/DB/realtime/deploy/auth) | Accepted (awaiting user confirmation) |
| [003](003-architecture.md) | System architecture & UniFi integration boundary | Accepted (awaiting user confirmation) |

## How code maps to these decisions

| Decision | Enforced by |
|---|---|
| One-way module dependencies (ADR-003 §1) | `scripts/check-imports.sh`, run in CI |
| Credentials never in git/log/DB (ADR-003 §4) | `.gitignore`, `scripts/check-secrets.sh`, `config.Redacted()`, logger redaction |
| Single error shape `{code, message, details}` (ADR-003 §5) | `apps/api/internal/api/errors.go`, `openapi/openapi.yaml` |
| Liveness/readiness split (ADR-003 §6) | `/api/v1/health` vs `/api/v1/ready` |
| Go 1.23 + chi + pgx, Postgres 16 (ADR-002) | `go.mod`, `docker-compose.yml` |
| React + Vite SPA (ADR-002) | `apps/web` |

## Adding an ADR

Copy the structure of an existing file: context → decision → consequences →
rejected alternatives. Add it to the table above.

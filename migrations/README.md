# Migrations

Forward-only migrations run with [`goose`](https://github.com/pressly/goose).

Naming: `NNNN_short_description.sql`, with `-- +goose Up` / `-- +goose Down`
annotations. Migrations must be backward compatible; no destructive change
without explicit approval (ADR-002 §3).

The initial schema (sites, devices, clients, networks, users, audit log,
sync_runs) is owned by **ZENI-5** and delivered as `0001_init.sql`. The design
rationale, ERD and source-of-truth mapping live in [`docs/data-model.md`](../docs/data-model.md).

Apply locally against the dev database:

```sh
make migrate
```

Or directly with `goose`:

```sh
go run github.com/pressly/goose/v3/cmd/goose@v3.22.1 \
  -dir ./migrations postgres "$DATABASE_URL" up
```

The `Down` section of a migration is for local/dev resets only. Production is
rolled back with a compensating forward migration — never `goose down` (see
[`docs/data-model.md`](../docs/data-model.md) §6).

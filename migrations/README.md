# Migrations

Forward-only migrations run with [`goose`](https://github.com/pressly/goose).

Naming: `NNNN_short_description.sql`, with `-- +goose Up` / `-- +goose Down`
annotations. Migrations must be backward compatible; no destructive change
without explicit approval (ADR-002 §3).

The initial schema (sites, devices, clients, networks, users, audit log,
sync_runs) is owned by **ZENI-5**. This directory is intentionally empty until
that issue delivers `0001_init.sql`.

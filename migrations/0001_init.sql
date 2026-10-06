-- 0001_init.sql — initial schema for the Network Orchestrator control plane.
--
-- Owner: ZENI-5 (DATA). Design doc: docs/data-model.md.
-- Spec: docs/adr/003-architecture.md §2 (domain types) & §3 (sync/cache).
--
-- Forward-only. The `Down` section exists for local/dev resets only; in
-- production a release is rolled back with a compensating forward migration,
-- never with `goose down` (see migrations/README.md).
--
-- Conventions:
--   * uuid PKs use Postgres 16 core `gen_random_uuid()`; no extension needed.
--   * Append-only logs (audit_log, client_events) use bigint identity for
--     stable insertion order.
--   * Cached UniFi rows always carry `source`, `synced_at` and `raw`.
--   * Controlled vocabularies are `text` + CHECK, not native enums: a value is
--     added/removed with an ALTER in a new migration, never a type rewrite.
--   * MAC/IP strings are normalized in the adapter (MAC lower-case, colon
--     separated) before they reach these tables.

-- +goose Up

-- ---------------------------------------------------------------------------
-- Cached UniFi state — `sites`
-- Source: UniFi. Integration API `/sites` (identity, via a console), enriched
-- by Site Manager cloud `meta` (name, timezone, gatewayMac).
-- ---------------------------------------------------------------------------
CREATE TABLE sites (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    unifi_site_id  text NOT NULL,             -- Integration API site id (stable natural key)
    console_id     text NOT NULL,             -- Site Manager host id; connector-proxy path param
    sm_site_id     text,                      -- Site Manager site id, if it differs from unifi_site_id
    name           text NOT NULL,
    description    text,
    timezone       text,
    gateway_mac    text,                      -- normalized, lower-case, colon separated
    source         text NOT NULL DEFAULT 'console',
    raw            jsonb NOT NULL DEFAULT '{}'::jsonb,
    synced_at      timestamptz NOT NULL,
    CONSTRAINT sites_source_valid CHECK (source IN ('cloud', 'console', 'both'))
);

-- Assumption: Integration API site ids are globally unique UUIDs. If a fleet
-- ever reuses ids across consoles, the key becomes (console_id, unifi_site_id)
-- in an additive follow-up migration.
CREATE UNIQUE INDEX sites_unifi_site_id_key ON sites (unifi_site_id);
CREATE INDEX sites_console_id_idx ON sites (console_id);

-- ---------------------------------------------------------------------------
-- Internal — `users` (dashboard operators). Source: internal.
-- ---------------------------------------------------------------------------
CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text NOT NULL,
    password_hash text NOT NULL,              -- argon2id; never a raw or reversible value
    role          text NOT NULL DEFAULT 'viewer',
    is_active     boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    CONSTRAINT users_email_lowercase CHECK (email = lower(email)),
    CONSTRAINT users_role_valid CHECK (role IN ('viewer', 'operator', 'admin'))
);
CREATE UNIQUE INDEX users_email_key ON users (email);

-- ---------------------------------------------------------------------------
-- Internal — `sessions` (server-side operator sessions). Source: internal.
-- The cookie carries an opaque token; only its SHA-256 lives here.
-- ---------------------------------------------------------------------------
CREATE TABLE sessions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   bytea NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz,
    ip           inet,
    user_agent   text,
    revoked_at   timestamptz
);
CREATE UNIQUE INDEX sessions_token_hash_key ON sessions (token_hash);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- ---------------------------------------------------------------------------
-- Internal — `audit_log` (append-only trail of operator/system actions).
-- Source: internal. UPDATE/DELETE are never issued by the application.
--
-- `actor_user_id` and `site_id` are intentionally NOT foreign keys: an audit
-- row is an immutable historical fact and must not be mutated or lost because a
-- user or a cache row is later removed. Snapshot labels are stored alongside.
-- ---------------------------------------------------------------------------
CREATE TABLE audit_log (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_user_id uuid,                       -- no FK: audit rows must survive user deletion
    actor_label   text,                       -- e.g. operator email at the time of the action
    actor_type    text NOT NULL DEFAULT 'user',
    action        text NOT NULL,              -- stable verb, e.g. 'auth.login', 'device.action.restart'
    target_type   text,                       -- e.g. 'device', 'client', 'session'
    target_id     text,
    site_id       uuid,                       -- no FK: survives site cache changes
    request_id    text,
    ip            inet,
    detail        jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT audit_log_actor_type_valid CHECK (actor_type IN ('user', 'system'))
);
CREATE INDEX audit_log_created_at_idx ON audit_log (created_at DESC);
CREATE INDEX audit_log_actor_user_id_idx ON audit_log (actor_user_id);
CREATE INDEX audit_log_target_idx ON audit_log (target_type, target_id);
CREATE INDEX audit_log_site_id_idx ON audit_log (site_id);

-- ---------------------------------------------------------------------------
-- Internal — `sync_runs` (one row per UniFi sync cycle). Source: internal.
-- `error_kind` maps onto the adapter taxonomy in internal/unifi; `error_message`
-- is sanitized (never carries a key, header or raw payload).
-- ---------------------------------------------------------------------------
CREATE TABLE sync_runs (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    surface           text NOT NULL,          -- which UniFi surface the cycle used
    trigger           text NOT NULL DEFAULT 'schedule',
    started_at        timestamptz NOT NULL DEFAULT now(),
    finished_at       timestamptz,
    duration_ms       integer,
    status            text NOT NULL DEFAULT 'running',
    sites_total       integer NOT NULL DEFAULT 0,
    sites_ok          integer NOT NULL DEFAULT 0,
    sites_failed      integer NOT NULL DEFAULT 0,
    devices_upserted  integer NOT NULL DEFAULT 0,
    clients_upserted  integer NOT NULL DEFAULT 0,
    networks_upserted integer NOT NULL DEFAULT 0,
    rate_limited      boolean NOT NULL DEFAULT false,
    retry_after_ms    integer,
    error_kind        text,
    error_message     text,
    request_id        text,
    CONSTRAINT sync_runs_surface_valid CHECK (surface IN ('cloud', 'console', 'mixed')),
    CONSTRAINT sync_runs_trigger_valid CHECK (trigger IN ('schedule', 'manual', 'startup')),
    CONSTRAINT sync_runs_status_valid CHECK (status IN ('running', 'success', 'partial', 'failed', 'skipped'))
);
CREATE INDEX sync_runs_started_at_idx ON sync_runs (started_at DESC);
CREATE INDEX sync_runs_status_idx ON sync_runs (status);

-- ---------------------------------------------------------------------------
-- Cached UniFi state — `devices` (AP / switch / gateway).
-- Source: UniFi. Integration API `/sites/{id}/devices` (+ `/{deviceId}`),
-- enriched by Site Manager cloud device summary.
-- ---------------------------------------------------------------------------
CREATE TABLE devices (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    site_id            uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    unifi_device_id    text NOT NULL,         -- Integration API device id (stable)
    mac                text,                  -- normalized MAC
    name               text,
    model              text,                  -- e.g. 'U6 Pro'
    device_type        text,                  -- derived from UniFi `features`
    status             text,                  -- UniFi `state`: ONLINE | OFFLINE | ...
    firmware_version   text,
    firmware_updatable boolean,
    ip_address         inet,
    adopted_at         timestamptz,
    provisioned_at     timestamptz,
    uplink_device_id   text,                  -- UniFi `uplink.deviceId`
    supported          boolean,
    features           jsonb,                 -- UniFi `features`
    radios             jsonb,                 -- UniFi `interfaces.radios` (array)
    uptime_sec         bigint,                -- see docs/data-model.md "unverified fields"
    source             text NOT NULL DEFAULT 'console',
    raw                jsonb NOT NULL DEFAULT '{}'::jsonb,
    synced_at          timestamptz NOT NULL,
    CONSTRAINT devices_type_valid CHECK (device_type IS NULL OR device_type IN ('ap', 'switch', 'gateway', 'other')),
    CONSTRAINT devices_source_valid CHECK (source IN ('cloud', 'console', 'both'))
);
CREATE UNIQUE INDEX devices_site_unifi_key ON devices (site_id, unifi_device_id);
CREATE INDEX devices_mac_idx ON devices (mac);
CREATE INDEX devices_site_status_idx ON devices (site_id, status);
CREATE INDEX devices_uplink_device_id_idx ON devices (uplink_device_id);

-- ---------------------------------------------------------------------------
-- Cached UniFi state — `clients` (connected clients). Source: UniFi.
-- Integration API `/sites/{id}/clients` returns only *active* clients, so this
-- table is a current-state cache enriched with first_seen/last_seen. History is
-- kept in `client_events`, not by snapshotting every poll.
--
-- `ssid` and `signal_dbm` are NOT provided by Integration API v1 (verified in
-- ADR-001); they stay nullable placeholders until a surface supplies them.
-- ---------------------------------------------------------------------------
CREATE TABLE clients (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    site_id          uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    unifi_client_id  text NOT NULL,           -- Integration API client id (stable)
    mac              text,
    hostname         text,                    -- UniFi `name`
    ip_address       inet,
    client_type      text,                    -- WIRELESS | WIRED
    access_type      text,                    -- UniFi `access.type`
    uplink_device_id text,                    -- AP/switch the client is attached to
    ssid             text,                    -- not available in v1 (nullable placeholder)
    signal_dbm       integer,                 -- not available in v1 (nullable placeholder)
    is_connected     boolean NOT NULL DEFAULT true,
    connected_at     timestamptz,
    disconnected_at  timestamptz,
    first_seen       timestamptz NOT NULL DEFAULT now(),
    last_seen        timestamptz NOT NULL DEFAULT now(),
    source           text NOT NULL DEFAULT 'console',
    raw              jsonb NOT NULL DEFAULT '{}'::jsonb,
    synced_at        timestamptz NOT NULL,
    CONSTRAINT clients_source_valid CHECK (source IN ('cloud', 'console', 'both'))
);
CREATE UNIQUE INDEX clients_site_unifi_key ON clients (site_id, unifi_client_id);
CREATE INDEX clients_mac_idx ON clients (mac);
CREATE INDEX clients_site_connected_idx ON clients (site_id, is_connected);
CREATE INDEX clients_uplink_device_id_idx ON clients (uplink_device_id);

-- ---------------------------------------------------------------------------
-- Internal — `client_events` (append-only connect/disconnect transitions).
-- Source: internal, derived by diffing the active-client list between cycles
-- and recorded only on a state change (bounded growth: O(transitions)).
-- ---------------------------------------------------------------------------
CREATE TABLE client_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    client_id   uuid NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    site_id     uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    event_type  text NOT NULL,
    occurred_at timestamptz NOT NULL,
    sync_run_id uuid REFERENCES sync_runs(id) ON DELETE SET NULL,
    CONSTRAINT client_events_type_valid CHECK (event_type IN ('connected', 'disconnected'))
);
CREATE INDEX client_events_client_id_idx ON client_events (client_id);
CREATE INDEX client_events_site_time_idx ON client_events (site_id, occurred_at DESC);

-- ---------------------------------------------------------------------------
-- Cached UniFi state — `networks` (VLAN / subnet / DHCP configuration).
-- Source: UniFi. Integration API `/sites/{id}/networks`.
-- NOTE: a UniFi *network* is not a *WLAN/SSID*. SSID + security mode are not
-- exposed by Integration API v1 (`/wifi` → 404); a `wifi_broadcasts` table is
-- deliberately deferred until a surface exists (docs/data-model.md).
-- ---------------------------------------------------------------------------
CREATE TABLE networks (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    site_id          uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    unifi_network_id text NOT NULL,
    name             text NOT NULL,
    vlan_id          integer,
    management       text,                    -- UniFi `management`, e.g. GATEWAY
    enabled          boolean,
    is_default       boolean,                 -- UniFi `default`
    origin           text,                    -- UniFi `metadata.origin`
    configurable     boolean,                 -- UniFi `metadata.configurable`
    source           text NOT NULL DEFAULT 'console',
    raw              jsonb NOT NULL DEFAULT '{}'::jsonb,
    synced_at        timestamptz NOT NULL,
    CONSTRAINT networks_source_valid CHECK (source IN ('cloud', 'console', 'both'))
);
CREATE UNIQUE INDEX networks_site_unifi_key ON networks (site_id, unifi_network_id);
CREATE INDEX networks_site_id_idx ON networks (site_id);

-- +goose Down

DROP TABLE IF EXISTS client_events;
DROP TABLE IF EXISTS networks;
DROP TABLE IF EXISTS clients;
DROP TABLE IF EXISTS devices;
DROP TABLE IF EXISTS sync_runs;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS sites;

-- Integration contracts: learn each event type's payload shape, then check
-- every event against it and open incidents for breaking changes.

-- Per-event result of the contract check.
--   none       not checked (no event type, not JSON, or rejected)
--   pending    queued for checking
--   learning   used to learn the shape (no active contract yet)
--   ok         matches the active contract
--   compatible only additive changes (e.g. a new optional field)
--   suspicious a warning (new enum value, type widened, non-critical change)
--   breaking   a critical field is missing, retyped or null
ALTER TABLE events ADD COLUMN contract_status text NOT NULL DEFAULT 'none';

CREATE TABLE contracts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    webhook_id      uuid NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    event_type      text NOT NULL,
    status          text NOT NULL DEFAULT 'learning' CHECK (status IN ('learning', 'proposed', 'active')),
    -- shape learned from samples since the contract was created or last activated
    observed        jsonb NOT NULL DEFAULT '{"samples": 0, "fields": {}}',
    active_version  integer,
    -- additive changes seen against the active version: path -> {count, first_seen, types}
    new_fields      jsonb NOT NULL DEFAULT '{}',
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (webhook_id, event_type)
);
CREATE INDEX contracts_org_idx ON contracts (org_id);

CREATE TABLE contract_versions (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    contract_id      uuid NOT NULL REFERENCES contracts (id) ON DELETE CASCADE,
    version          integer NOT NULL,
    schema           jsonb NOT NULL,
    fingerprint      text NOT NULL,
    critical_fields  text[] NOT NULL DEFAULT '{}',
    created_by       text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (contract_id, version)
);

-- Suspicious and breaking findings, one row per (event, path, kind).
CREATE TABLE contract_violations (
    id                 bigserial PRIMARY KEY,
    org_id             uuid NOT NULL,
    contract_id        uuid NOT NULL REFERENCES contracts (id) ON DELETE CASCADE,
    version            integer NOT NULL,
    event_id           uuid NOT NULL,
    event_received_at  timestamptz NOT NULL,
    severity           text NOT NULL CHECK (severity IN ('suspicious', 'breaking')),
    kind               text NOT NULL,
    path               text NOT NULL,
    expected           text NOT NULL DEFAULT '',
    actual             text NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX contract_violations_contract_idx ON contract_violations (contract_id, created_at DESC);
CREATE INDEX contract_violations_event_idx ON contract_violations (event_id);

-- Breaking findings grouped into incidents: one open incident per (contract, kind, path).
CREATE TABLE incidents (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    webhook_id       uuid NOT NULL,
    contract_id      uuid NOT NULL REFERENCES contracts (id) ON DELETE CASCADE,
    kind             text NOT NULL,
    path             text NOT NULL,
    severity         text NOT NULL DEFAULT 'breaking',
    status           text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    title            text NOT NULL,
    expected         text NOT NULL DEFAULT '',
    actual           text NOT NULL DEFAULT '',
    event_count      integer NOT NULL DEFAULT 0,
    first_seen_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at     timestamptz NOT NULL DEFAULT now(),
    sample_event_id  uuid,
    resolved_at      timestamptz,
    resolved_by      text NOT NULL DEFAULT '',
    resolution       text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX incidents_open_key ON incidents (contract_id, kind, path) WHERE status = 'open';
CREATE INDEX incidents_org_idx ON incidents (org_id, status, last_seen_at DESC);

-- Events waiting to be checked (filled by ingest in the event's transaction).
CREATE TABLE contract_queue (
    event_id           uuid PRIMARY KEY,
    event_received_at  timestamptz NOT NULL,
    org_id             uuid NOT NULL,
    webhook_id         uuid NOT NULL,
    event_type         text NOT NULL,
    enqueued_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX contract_queue_enqueued_idx ON contract_queue (enqueued_at);

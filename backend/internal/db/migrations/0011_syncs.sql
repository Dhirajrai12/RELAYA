-- Syncs: pull records from a connection on a schedule; new and changed records
-- become events on a webhook (so they are delivered, checked and replayable
-- like any other event).

CREATE TABLE syncs (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id                uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    connection_id         uuid NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    -- where the events go: its destinations get them, its contracts check them
    webhook_id            uuid NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    model                 text NOT NULL,
    config                jsonb NOT NULL DEFAULT '{}',
    interval_seconds      integer NOT NULL CHECK (interval_seconds >= 60),
    enabled               boolean NOT NULL DEFAULT true,
    -- first run: false = only remember existing records; true = send events for them too
    emit_existing         boolean NOT NULL DEFAULT false,
    baseline_done         boolean NOT NULL DEFAULT false,
    cursor                text NOT NULL DEFAULT '',
    next_run_at           timestamptz NOT NULL DEFAULT now(),
    run_started_at        timestamptz, -- set while a worker runs it
    last_run_at           timestamptz,
    last_status           text NOT NULL DEFAULT 'never' CHECK (last_status IN ('never', 'ok', 'error')),
    last_error            text NOT NULL DEFAULT '',
    consecutive_failures  integer NOT NULL DEFAULT 0,
    records               integer NOT NULL DEFAULT 0, -- records known
    events                bigint NOT NULL DEFAULT 0,  -- events produced
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX syncs_org_idx ON syncs (org_id, created_at);
CREATE INDEX syncs_due_idx ON syncs (next_run_at) WHERE enabled;

-- What each record looked like last time (a hash), to tell new from changed.
CREATE TABLE sync_records (
    sync_id       uuid NOT NULL REFERENCES syncs (id) ON DELETE CASCADE,
    record_id     text NOT NULL,
    hash          text NOT NULL,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    changed_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (sync_id, record_id)
);

CREATE TABLE sync_runs (
    id           bigserial PRIMARY KEY,
    org_id       uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    sync_id      uuid NOT NULL REFERENCES syncs (id) ON DELETE CASCADE,
    started_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz,
    status       text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'ok', 'error')),
    fetched      integer NOT NULL DEFAULT 0,
    created      integer NOT NULL DEFAULT 0,
    updated      integer NOT NULL DEFAULT 0,
    error        text NOT NULL DEFAULT ''
);
CREATE INDEX sync_runs_sync_idx ON sync_runs (sync_id, started_at DESC);

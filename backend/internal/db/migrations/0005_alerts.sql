-- Alerts: notify Slack, email or a webhook when incidents open/resolve,
-- destinations start/stop failing, or signature checks fail.

CREATE TABLE alert_channels (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    type        text NOT NULL CHECK (type IN ('slack', 'email', 'webhook')),
    name        text NOT NULL,
    -- what the UI shows: an email address, or a masked URL
    target      text NOT NULL,
    -- slack/webhook URL, encrypted with the org's data key
    url_enc     bytea,
    -- webhook channels: secret for the Relaya-Signature header
    secret_enc  bytea,
    events      text[] NOT NULL,
    enabled     boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX alert_channels_org_idx ON alert_channels (org_id);

-- Outbox + log: one row per (alert, channel), written in the same transaction
-- as the change that triggered it, sent by the worker.
CREATE TABLE alerts (
    id               bigserial PRIMARY KEY,
    org_id           uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    channel_id       uuid NOT NULL REFERENCES alert_channels (id) ON DELETE CASCADE,
    kind             text NOT NULL,
    title            text NOT NULL,
    body             text NOT NULL DEFAULT '',
    link             text NOT NULL DEFAULT '', -- dashboard path, e.g. /incidents
    status           text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    attempts         integer NOT NULL DEFAULT 0,
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    last_error       text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    sent_at          timestamptz
);
CREATE INDEX alerts_due_idx ON alerts (next_attempt_at) WHERE status = 'pending';
CREATE INDEX alerts_org_idx ON alerts (org_id, created_at DESC);

-- Destination health for "failing" / "recovered" alerts (one alert per outage).
ALTER TABLE destinations ADD COLUMN consecutive_failures integer NOT NULL DEFAULT 0;
ALTER TABLE destinations ADD COLUMN health text NOT NULL DEFAULT 'ok' CHECK (health IN ('ok', 'failing'));

-- Throttle for signature-failure alerts (at most one per webhook per hour).
ALTER TABLE webhooks ADD COLUMN last_signature_alert_at timestamptz;

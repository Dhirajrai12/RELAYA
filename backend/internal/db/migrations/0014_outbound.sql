-- Outbound webhooks: an organization sends events to its own customers'
-- endpoints through Relaya. Each customer is an "app" with its own hidden
-- webhook; its endpoints are destinations on that webhook, so messages go
-- through the normal delivery engine (retries, attempt log, alerts, replay).

-- Inbound webhooks receive from providers; outbound ones hold messages an org
-- sends (they have no usable ingest URL).
ALTER TABLE webhooks ADD COLUMN kind text NOT NULL DEFAULT 'inbound' CHECK (kind IN ('inbound', 'outbound'));

-- Which event types a destination gets (empty = all), and how requests are
-- signed: Relaya-Signature, or the Standard Webhooks headers (outbound endpoints).
ALTER TABLE destinations ADD COLUMN event_types text[] NOT NULL DEFAULT '{}';
ALTER TABLE destinations ADD COLUMN signing_scheme text NOT NULL DEFAULT 'relaya' CHECK (signing_scheme IN ('relaya', 'standard'));

CREATE TABLE outbound_apps (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    -- the org's own ID for its customer, e.g. "acme" or a UUID
    uid         text NOT NULL,
    name        text NOT NULL,
    webhook_id  uuid NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, uid)
);

-- The event types an org sends, shown in the portal as choices for endpoints.
CREATE TABLE outbound_event_types (
    org_id       uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name         text NOT NULL,
    description  text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, name)
);

-- A portal link: lets one app's user manage its endpoints for a while.
CREATE TABLE portal_sessions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    app_id      uuid NOT NULL REFERENCES outbound_apps (id) ON DELETE CASCADE,
    token_hash  text NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    created_by  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX portal_sessions_expires_idx ON portal_sessions (expires_at);

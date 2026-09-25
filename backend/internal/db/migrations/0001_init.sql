-- Month 1 foundation: workspace, auth, vault keys, webhook gateway, event store, audit log.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Workspace -----------------------------------------------------------------

CREATE TABLE organizations (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    slug        text NOT NULL UNIQUE,
    plan        text NOT NULL DEFAULT 'developer'
                CHECK (plan IN ('developer', 'team', 'business', 'enterprise')),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email          text NOT NULL,
    name           text NOT NULL DEFAULT '',
    password_hash  text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

-- Three roles at launch: owner > admin > member.
CREATE TABLE memberships (
    org_id      uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role        text NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX memberships_user_idx ON memberships (user_id);

CREATE TABLE sessions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash  bytea NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE api_keys (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name          text NOT NULL,
    prefix        text NOT NULL,
    key_hash      bytea NOT NULL UNIQUE,
    role          text NOT NULL CHECK (role IN ('admin', 'member')),
    created_by    uuid REFERENCES users (id) ON DELETE SET NULL,
    last_used_at  timestamptz,
    revoked_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX api_keys_org_idx ON api_keys (org_id);

-- Envelope encryption: one data key per organization, wrapped by the master key.
CREATE TABLE org_keys (
    org_id       uuid PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    wrapped_dek  bytea NOT NULL,
    master_kid   text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE projects (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name        text NOT NULL,
    slug        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);

-- Webhook gateway -------------------------------------------------------------

-- A webhook is one inbound URL (/v1/in/{ingest_token}) that a provider posts to.
CREATE TABLE webhooks (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id          uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name                text NOT NULL,
    provider            text NOT NULL DEFAULT 'generic',
    ingest_token        text NOT NULL UNIQUE,
    signing_secret_enc  bytea,
    -- generic provider only: header carrying a hex HMAC-SHA256 of the body
    signature_header    text NOT NULL DEFAULT '',
    status              text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused')),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX webhooks_project_idx ON webhooks (project_id);

-- Events are partitioned monthly by received_at.
CREATE TABLE events (
    id               uuid NOT NULL DEFAULT gen_random_uuid(),
    org_id           uuid NOT NULL,
    project_id       uuid NOT NULL,
    webhook_id       uuid NOT NULL,
    dedup_key        text NOT NULL,
    type             text NOT NULL DEFAULT '',
    status           text NOT NULL
                     CHECK (status IN ('received', 'rejected')),
    signature        text NOT NULL
                     CHECK (signature IN ('valid', 'invalid', 'missing', 'not_configured')),
    content_type     text NOT NULL DEFAULT '',
    headers          jsonb NOT NULL DEFAULT '{}',
    payload          bytea,
    payload_size     integer NOT NULL DEFAULT 0,
    s3_key           text,
    source_ip        inet,
    received_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, received_at)
) PARTITION BY RANGE (received_at);

CREATE TABLE events_default PARTITION OF events DEFAULT;

CREATE INDEX events_org_received_idx ON events (org_id, received_at DESC, id DESC);
CREATE INDEX events_webhook_received_idx ON events (webhook_id, received_at DESC);
CREATE INDEX events_org_type_idx ON events (org_id, type, received_at DESC);

-- Dedup lives outside the partitioned table so uniqueness holds across months.
CREATE TABLE event_dedup (
    webhook_id   uuid NOT NULL,
    dedup_key    text NOT NULL,
    event_id     uuid NOT NULL,
    received_at  timestamptz NOT NULL,
    duplicates   integer NOT NULL DEFAULT 0,
    PRIMARY KEY (webhook_id, dedup_key)
);
CREATE INDEX event_dedup_received_idx ON event_dedup (received_at);

-- Creates the monthly partitions for [month of from_ts, +months_ahead].
CREATE FUNCTION ensure_event_partitions(from_ts timestamptz, months_ahead int)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    start_month date := date_trunc('month', from_ts)::date;
    m date;
    part text;
BEGIN
    FOR i IN 0..months_ahead LOOP
        m := (start_month + make_interval(months => i))::date;
        part := format('events_%s', to_char(m, 'YYYY_MM'));
        IF to_regclass(part) IS NULL THEN
            EXECUTE format(
                'CREATE TABLE %I PARTITION OF events FOR VALUES FROM (%L) TO (%L)',
                part, m, (m + interval '1 month')::date);
        END IF;
    END LOOP;
END $$;

SELECT ensure_event_partitions(now(), 2);

-- Audit -----------------------------------------------------------------------

CREATE TABLE audit_logs (
    id           bigserial PRIMARY KEY,
    org_id       uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    actor_type   text NOT NULL CHECK (actor_type IN ('user', 'api_key', 'system')),
    actor_id     text NOT NULL DEFAULT '',
    action       text NOT NULL,
    target_type  text NOT NULL DEFAULT '',
    target_id    text NOT NULL DEFAULT '',
    reason       text NOT NULL DEFAULT '',
    result       text NOT NULL DEFAULT 'success',
    metadata     jsonb NOT NULL DEFAULT '{}',
    at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_org_at_idx ON audit_logs (org_id, at DESC);

-- Connections: your users connect their accounts at other apps (Zoho, HubSpot,
-- Google, Shiprocket…) through a hosted Connect page; Relaya stores the tokens
-- encrypted and keeps them fresh.

-- One per provider app an organization configures: its own OAuth client, or
-- nothing for login-based providers.
CREATE TABLE integrations (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    -- the name API callers use, e.g. "zoho" or "zoho-books"
    key                text NOT NULL,
    provider           text NOT NULL,
    name               text NOT NULL,
    client_id          text NOT NULL DEFAULT '',
    client_secret_enc  bytea,
    scopes             text[] NOT NULL DEFAULT '{}',
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, key)
);

-- One per end user (your customer's user) per integration.
CREATE TABLE connections (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    integration_id     uuid NOT NULL REFERENCES integrations (id) ON DELETE CASCADE,
    -- your identifier for the user or account that connected
    end_user_id        text NOT NULL,
    status             text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'broken')),
    -- tokens (and login credentials for login-based providers), encrypted with the org's data key
    credentials_enc    bytea NOT NULL,
    -- when the access token expires (NULL = no expiry known); drives refreshing
    expires_at         timestamptz,
    -- non-secret facts from the provider, e.g. Zoho's api_domain, granted scopes
    metadata           jsonb NOT NULL DEFAULT '{}',
    last_refreshed_at  timestamptz,
    refresh_failures   integer NOT NULL DEFAULT 0,
    last_error         text NOT NULL DEFAULT '',
    broken_at          timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (integration_id, end_user_id)
);
CREATE INDEX connections_org_idx ON connections (org_id, created_at DESC);
CREATE INDEX connections_refresh_idx ON connections (expires_at) WHERE status = 'active';

-- A Connect link: valid for 30 minutes, used once.
CREATE TABLE connect_sessions (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    integration_id  uuid NOT NULL REFERENCES integrations (id) ON DELETE CASCADE,
    end_user_id     text NOT NULL,
    -- sha256 of the link token; the token itself is only in the link
    token_hash      text NOT NULL UNIQUE,
    -- OAuth state and PKCE verifier, set when the user starts the provider login
    state           text UNIQUE,
    code_verifier   text NOT NULL DEFAULT '',
    return_url      text NOT NULL DEFAULT '',
    expires_at      timestamptz NOT NULL,
    completed_at    timestamptz,
    connection_id   uuid,
    error           text NOT NULL DEFAULT '',
    created_by      text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX connect_sessions_created_idx ON connect_sessions (created_at);

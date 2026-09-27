-- Proxy: API calls made through a connection. Logged without query strings or
-- bodies (they can hold personal data); kept 30 days.
CREATE TABLE proxy_calls (
    id             bigserial PRIMARY KEY,
    org_id         uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    connection_id  uuid NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    method         text NOT NULL,
    host           text NOT NULL,
    path           text NOT NULL,
    status         integer NOT NULL DEFAULT 0, -- 0: no answer from the provider
    attempts       integer NOT NULL DEFAULT 1,
    duration_ms    integer NOT NULL DEFAULT 0,
    error          text NOT NULL DEFAULT '',
    actor          text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX proxy_calls_org_idx ON proxy_calls (org_id, created_at DESC);
CREATE INDEX proxy_calls_connection_idx ON proxy_calls (connection_id, created_at DESC);

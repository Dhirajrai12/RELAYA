-- Jira alert channels: an incident becomes a Jira issue, repeats become comments,
-- and the recovery comments on it and moves it to Done.

ALTER TABLE alert_channels DROP CONSTRAINT IF EXISTS alert_channels_type_check;
ALTER TABLE alert_channels ADD CONSTRAINT alert_channels_type_check CHECK (type IN ('slack', 'email', 'webhook', 'jira'));

-- Non-secret settings, e.g. jira: {"site", "email", "project", "issue_type"}. The API token is in secret_enc.
ALTER TABLE alert_channels ADD COLUMN config jsonb NOT NULL DEFAULT '{}';

-- What an alert is about, e.g. "incident:<id>" or "destination:<id>", so a recovery
-- finds the issue its failure opened. Empty for alerts about nothing in particular.
ALTER TABLE alerts ADD COLUMN subject text NOT NULL DEFAULT '';
-- Where the channel put it, e.g. a Jira issue key.
ALTER TABLE alerts ADD COLUMN external_ref text NOT NULL DEFAULT '';

-- Issues Relaya opened in a ticketing channel, one open issue per (channel, subject).
CREATE TABLE alert_issues (
    id          bigserial PRIMARY KEY,
    channel_id  uuid NOT NULL REFERENCES alert_channels (id) ON DELETE CASCADE,
    subject     text NOT NULL,
    issue_key   text NOT NULL,
    opened_at   timestamptz NOT NULL DEFAULT now(),
    closed_at   timestamptz
);
CREATE UNIQUE INDEX alert_issues_open_idx ON alert_issues (channel_id, subject) WHERE closed_at IS NULL;

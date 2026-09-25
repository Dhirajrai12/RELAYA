-- Repair rules: ordered edits applied to a provider's payload before it is
-- forwarded, so endpoints keep getting the shape they expect while the
-- provider fixes their side. The stored event is never changed.

CREATE TABLE repair_rules (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    webhook_id       uuid NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    event_type       text NOT NULL DEFAULT '', -- '' = every event type
    name             text NOT NULL,
    ops              jsonb NOT NULL,
    enabled          boolean NOT NULL DEFAULT true,
    position         integer NOT NULL DEFAULT 0, -- rules run in (position, created_at) order
    incident_id      uuid REFERENCES incidents (id) ON DELETE SET NULL, -- created to fix this incident
    applied_count    bigint NOT NULL DEFAULT 0,
    last_applied_at  timestamptz,
    created_by       text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX repair_rules_webhook_idx ON repair_rules (webhook_id, position, created_at) WHERE enabled;
CREATE INDEX repair_rules_org_idx ON repair_rules (org_id);

-- Which rules changed the body of an attempt (names at send time, for the attempt log).
ALTER TABLE delivery_attempts ADD COLUMN repaired_by text[] NOT NULL DEFAULT '{}';

-- A violation a repair rule fixed: recorded for visibility, but it opens no incident.
ALTER TABLE contract_violations ADD COLUMN repaired boolean NOT NULL DEFAULT false;

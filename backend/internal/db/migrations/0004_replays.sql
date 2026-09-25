-- Recovery: replay the events affected by an incident to their destinations,
-- track the result, and close the incident once every replayed delivery succeeded.

CREATE TABLE replays (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    incident_id   uuid REFERENCES incidents (id) ON DELETE SET NULL,
    status        text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'completed')),
    total         integer NOT NULL,
    succeeded     integer NOT NULL DEFAULT 0,
    failed        integer NOT NULL DEFAULT 0,
    created_by    text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    completed_at  timestamptz
);
CREATE INDEX replays_incident_idx ON replays (incident_id, created_at DESC);
-- At most one running replay per incident.
CREATE UNIQUE INDEX replays_one_running ON replays (incident_id) WHERE status = 'running';

-- A delivery re-queued by a replay points at it until it finishes.
ALTER TABLE deliveries ADD COLUMN replay_id uuid REFERENCES replays (id) ON DELETE SET NULL;
ALTER TABLE delivery_attempts ADD COLUMN replay_id uuid;

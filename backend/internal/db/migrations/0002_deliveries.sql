-- Outbound delivery: forward received events to customer endpoints with retries.

-- A destination is a customer endpoint that receives every accepted event of one webhook.
CREATE TABLE destinations (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    webhook_id          uuid NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    name                text NOT NULL,
    url                 text NOT NULL,
    enabled             boolean NOT NULL DEFAULT true,
    -- secret Relaya signs outgoing requests with (Relaya-Signature header)
    signing_secret_enc  bytea NOT NULL,
    timeout_ms          integer NOT NULL DEFAULT 10000 CHECK (timeout_ms BETWEEN 1000 AND 30000),
    max_attempts        integer NOT NULL DEFAULT 8 CHECK (max_attempts BETWEEN 1 AND 20),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX destinations_webhook_idx ON destinations (webhook_id);

-- One row per (event, destination): the delivery job and its current state.
-- The unique constraint is the dedup guarantee: an event is delivered once per destination.
CREATE TABLE deliveries (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL,
    event_id           uuid NOT NULL,
    event_received_at  timestamptz NOT NULL, -- lets event lookups prune partitions
    webhook_id         uuid NOT NULL,
    destination_id     uuid NOT NULL REFERENCES destinations (id) ON DELETE CASCADE,
    status             text NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending', 'in_flight', 'retrying', 'succeeded', 'failed')),
    attempts           integer NOT NULL DEFAULT 0,
    next_attempt_at    timestamptz NOT NULL DEFAULT now(),
    locked_until       timestamptz, -- in_flight lease; expired leases are picked up again
    last_status_code   integer,
    last_error         text NOT NULL DEFAULT '',
    last_attempt_at    timestamptz,
    completed_at       timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, destination_id)
);
CREATE INDEX deliveries_due_idx ON deliveries (next_attempt_at) WHERE status IN ('pending', 'retrying');
CREATE INDEX deliveries_lease_idx ON deliveries (locked_until) WHERE status = 'in_flight';
CREATE INDEX deliveries_event_idx ON deliveries (event_id);
CREATE INDEX deliveries_destination_idx ON deliveries (destination_id, created_at DESC);
CREATE INDEX deliveries_org_created_idx ON deliveries (org_id, created_at DESC);

-- Full history: one row per HTTP attempt.
CREATE TABLE delivery_attempts (
    id             bigserial PRIMARY KEY,
    delivery_id    uuid NOT NULL REFERENCES deliveries (id) ON DELETE CASCADE,
    attempt        integer NOT NULL,
    started_at     timestamptz NOT NULL,
    duration_ms    integer NOT NULL,
    status_code    integer,
    error          text NOT NULL DEFAULT '',
    response_body  text NOT NULL DEFAULT '', -- first 4 KiB, masked
    outcome        text NOT NULL CHECK (outcome IN ('succeeded', 'retry', 'failed'))
);
CREATE INDEX delivery_attempts_delivery_idx ON delivery_attempts (delivery_id, attempt);

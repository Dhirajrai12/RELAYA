-- Indexes for the retention job (deletes by event time in batches) and for metrics.
CREATE INDEX IF NOT EXISTS events_received_idx ON events (received_at);
CREATE INDEX IF NOT EXISTS deliveries_event_received_idx ON deliveries (event_received_at);
CREATE INDEX IF NOT EXISTS contract_violations_event_received_idx ON contract_violations (event_received_at);
CREATE INDEX IF NOT EXISTS alerts_created_idx ON alerts (created_at);
CREATE INDEX IF NOT EXISTS sessions_expires_idx ON sessions (expires_at);
CREATE INDEX IF NOT EXISTS delivery_attempts_started_idx ON delivery_attempts (started_at); -- metrics: attempts in the last 5 minutes

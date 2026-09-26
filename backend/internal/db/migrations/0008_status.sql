-- Public status page: one sample per component per minute, written by the worker.
CREATE TABLE status_samples (
    component  text NOT NULL,
    at         timestamptz NOT NULL, -- truncated to the minute
    ok         boolean NOT NULL,
    PRIMARY KEY (component, at)
);
CREATE INDEX status_samples_at_idx ON status_samples (at);

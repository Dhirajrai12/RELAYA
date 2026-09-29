-- Events sent with the event simulator: labelled in the dashboard and kept out
-- of contract learning, so sample data never teaches a contract.
ALTER TABLE events ADD COLUMN simulated boolean NOT NULL DEFAULT false;

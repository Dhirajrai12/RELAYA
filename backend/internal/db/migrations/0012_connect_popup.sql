-- Connect links opened in a popup (connect.js): the result page closes itself.
ALTER TABLE connect_sessions ADD COLUMN popup boolean NOT NULL DEFAULT false;

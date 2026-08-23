-- Migration 0002: mark messages whose generation was cut short
-- (canceled or failed mid-stream), per dev spec §10 step 5.

ALTER TABLE messages ADD COLUMN truncated INTEGER NOT NULL DEFAULT 0;

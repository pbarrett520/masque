-- Migration 0003: persist the context-inspector record (dev spec §9)
-- on assistant messages: prompt segment breakdown, raw provider request
-- JSON, and the sampler param report, captured at generation time.

ALTER TABLE messages ADD COLUMN prompt_json TEXT;

-- Migration 0004: cloud providers became presets (internal/presets).
-- Before, the single "openai" provider meant "any OpenAI-compatible
-- endpoint" with a user-set base URL. Now "openai" is OpenAI itself and
-- bring-your-own endpoints live under "custom-openai". Installs that
-- pointed the old slot somewhere other than OpenAI move to the custom
-- preset so their chats keep talking to the same server.

INSERT INTO settings (key, value)
    SELECT 'provider.custom-openai.base_url', value FROM settings
    WHERE key = 'provider.openai.base_url'
      AND value NOT IN ('"https://api.openai.com/v1"', '"https://api.openai.com/v1/"', '""')
    ON CONFLICT(key) DO NOTHING;

INSERT INTO settings (key, value)
    SELECT 'provider.custom-openai.api_key', value FROM settings
    WHERE key = 'provider.openai.api_key'
      AND EXISTS (SELECT 1 FROM settings WHERE key = 'provider.custom-openai.base_url')
    ON CONFLICT(key) DO NOTHING;

UPDATE chats SET provider_id = 'custom-openai'
    WHERE provider_id = 'openai'
      AND EXISTS (SELECT 1 FROM settings WHERE key = 'provider.custom-openai.base_url');

UPDATE settings SET value = '"custom-openai"'
    WHERE key = 'provider.default_id' AND value = '"openai"'
      AND EXISTS (SELECT 1 FROM settings WHERE key = 'provider.custom-openai.base_url');

DELETE FROM settings
    WHERE key IN ('provider.openai.base_url', 'provider.openai.api_key')
      AND EXISTS (SELECT 1 FROM settings WHERE key = 'provider.custom-openai.base_url');

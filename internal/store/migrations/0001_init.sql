-- Migration 0001: initial schema (dev spec §6).
-- Never edit a shipped migration; add a new numbered file instead.

CREATE TABLE characters (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    card_json TEXT NOT NULL,         -- full V2/V3 card, canonical source
    avatar BLOB,                     -- extracted PNG, nullable
    created_at INTEGER, updated_at INTEGER
);

CREATE TABLE personas (              -- the user's own identity in chats
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    is_default INTEGER DEFAULT 0
);

CREATE TABLE chats (
    id INTEGER PRIMARY KEY,
    character_id INTEGER REFERENCES characters(id),
    persona_id INTEGER REFERENCES personas(id),
    title TEXT,
    provider_id TEXT, model TEXT,    -- last used, for resume
    params_json TEXT,                -- sampler overrides for this chat
    created_at INTEGER, updated_at INTEGER
);

CREATE TABLE messages (
    id INTEGER PRIMARY KEY,
    chat_id INTEGER REFERENCES chats(id),
    role TEXT CHECK(role IN ('user','assistant','system')),
    content TEXT NOT NULL,
    swipe_group INTEGER,             -- regenerations share a group; active one flagged
    is_active INTEGER DEFAULT 1,
    token_estimate INTEGER,
    created_at INTEGER
);

CREATE TABLE settings (              -- key/value, JSON values
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
-- provider configs (keys, base URLs) live in settings as JSON;
-- on macOS/Windows, API keys additionally go through OS keychain when available (M1.6 stretch)

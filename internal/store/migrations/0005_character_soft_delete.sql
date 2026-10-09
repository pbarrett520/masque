-- Migration 0005: deleting a character keeps its chats. deleted_at
-- marks the character hidden from the library; its row, card, and
-- avatar stay so old chats remain readable (labelled as a deleted
-- character, not continuable).

ALTER TABLE characters ADD COLUMN deleted_at INTEGER;

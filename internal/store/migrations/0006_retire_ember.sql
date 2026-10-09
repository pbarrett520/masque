-- Migration 0006: retire the built-in "Ember" starter in favour of the
-- bundled starter characters (internal/starters). The old seed row is
-- identified by the seed.ember_character_id setting and recognised as
-- untouched when its card_json still equals the exact bytes the seed
-- function wrote (a deterministic JSON marshal). Untouched Ember with no
-- chats is removed; untouched Ember with chats is soft-deleted so the
-- chats stay readable (migration 0005 semantics); an edited Ember is the
-- user's own character now and is kept. The setting is dropped either
-- way so nothing re-seeds her.

CREATE TEMP TABLE ember AS
    SELECT id FROM characters
    WHERE id = (SELECT CAST(value AS INTEGER) FROM settings WHERE key = 'seed.ember_character_id')
      AND card_json = '{"data":{"alternate_greetings":[],"character_version":"1.0","creator":"masque","creator_notes":"Masque''s built-in starter character.","description":"{{char}} is the keeper of the Lantern \u0026 Ledger, a snug tavern that appears at crossroads for travelers who need it. She has ash-grey hair pinned with a brass key, keeps a ledger no one is allowed to read, and always seems to have been expecting you.","extensions":{},"first_mes":"*The door creaks shut behind {{user}}, and the cold stays outside where it belongs. {{char}} sets down the glass she was polishing and smiles like she''s been waiting all evening.*\n\n\"There you are. Sit anywhere you like — the fire''s warmest by the window. Long road?\"","group_only_greetings":[],"mes_example":"","name":"Ember","personality":"warm, observant, quietly mischievous; asks good questions and remembers every answer","post_history_instructions":"","scenario":"{{user}} has just pushed open the tavern door on a cold night. The fire is lit, the room is otherwise empty, and {{char}} is polishing a glass behind the bar.","system_prompt":"","tags":["starter"]},"spec":"chara_card_v3","spec_version":"3.0"}';

DELETE FROM characters
    WHERE id IN (SELECT id FROM ember)
      AND NOT EXISTS (SELECT 1 FROM chats WHERE chats.character_id = characters.id);

UPDATE characters SET deleted_at = strftime('%s', 'now'), updated_at = strftime('%s', 'now')
    WHERE id IN (SELECT id FROM ember) AND deleted_at IS NULL;

DROP TABLE ember;

DELETE FROM settings WHERE key = 'seed.ember_character_id';

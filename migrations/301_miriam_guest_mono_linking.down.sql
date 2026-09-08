DROP INDEX IF EXISTS mono_linked_accounts_guest_token_idx;

ALTER TABLE mono_linked_accounts DROP COLUMN IF EXISTS guest_token;

-- Only restore NOT NULL once no guest rows remain (guests are attached to a
-- user before this migration is ever rolled back in practice).
DELETE FROM mono_linked_accounts WHERE user_id IS NULL;
ALTER TABLE mono_linked_accounts ALTER COLUMN user_id SET NOT NULL;

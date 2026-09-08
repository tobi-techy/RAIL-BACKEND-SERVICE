-- Guest (pre-signup) Mono linking.
--
-- Lets a person link their bank through Miriam's chat before they have a
-- RAIL account, so the "aha" financial picture can drive the conversation.
-- A linked account can exist with no user yet (user_id NULL) and is claimed
-- by a real user at signup. The global unique on mono_account_id means a
-- guest-linked account is attached to the user by UPDATE, never re-inserted.

ALTER TABLE mono_linked_accounts ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE mono_linked_accounts ADD COLUMN guest_token TEXT;

CREATE UNIQUE INDEX mono_linked_accounts_guest_token_idx ON mono_linked_accounts(guest_token);

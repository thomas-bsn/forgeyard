-- An app stopped by Forgeyard after crashing again and again, until someone starts it.
ALTER TABLE apps ADD COLUMN crash_suspended INTEGER NOT NULL DEFAULT 0;

-- A user's own Discord webhook, encrypted, for alerts about their apps.
ALTER TABLE users ADD COLUMN notify_webhook TEXT NOT NULL DEFAULT '';

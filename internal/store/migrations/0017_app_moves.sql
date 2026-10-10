-- An app moving to another node keeps running on the previous one until it is online on the new one.
ALTER TABLE apps ADD COLUMN moving_from INTEGER NOT NULL DEFAULT 0;
ALTER TABLE apps ADD COLUMN moved_at INTEGER NOT NULL DEFAULT 0;

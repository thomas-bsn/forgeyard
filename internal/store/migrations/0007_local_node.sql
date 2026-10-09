-- The node running on Forgeyard's own machine, joined automatically by the agent of the Compose project.
ALTER TABLE nodes ADD COLUMN is_local INTEGER NOT NULL DEFAULT 0;

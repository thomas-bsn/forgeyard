-- Why the node's agent could not reach Docker, shown on the node's card.
ALTER TABLE nodes ADD COLUMN docker_error TEXT NOT NULL DEFAULT '';

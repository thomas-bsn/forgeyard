-- An app built from a Dockerfile: its node builds the image, tagged with the apps.image name, instead of
-- pulling it.
ALTER TABLE apps ADD COLUMN dockerfile TEXT NOT NULL DEFAULT '';

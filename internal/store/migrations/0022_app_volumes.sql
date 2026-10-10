-- The paths of an app's container kept across redeployments, as a JSON array ("/var/lib/postgresql/data").
-- Each is a Docker volume of the app's node, named after the app and the path, deleted with the app.
ALTER TABLE apps ADD COLUMN volumes TEXT NOT NULL DEFAULT '[]';

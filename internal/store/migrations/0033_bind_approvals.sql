ALTER TABLE apps ADD COLUMN bind_mounts TEXT NOT NULL DEFAULT '[]';
ALTER TABLE apps ADD COLUMN bind_authorized INTEGER NOT NULL DEFAULT 1;
ALTER TABLE bind_requests ADD COLUMN app_id INTEGER REFERENCES apps(id) ON DELETE SET NULL;
ALTER TABLE bind_requests ADD COLUMN approved_mounts TEXT NOT NULL DEFAULT '[]';
CREATE UNIQUE INDEX bind_request_app ON bind_requests(app_id) WHERE app_id IS NOT NULL;

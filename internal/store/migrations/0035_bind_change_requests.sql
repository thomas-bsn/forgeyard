ALTER TABLE bind_requests ADD COLUMN change_resource_type TEXT NOT NULL DEFAULT '';
ALTER TABLE bind_requests ADD COLUMN change_resource_id INTEGER NOT NULL DEFAULT 0;
CREATE INDEX bind_request_change_resource ON bind_requests(change_resource_type, change_resource_id);

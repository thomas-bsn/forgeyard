-- Where an app's port (apps.port, the one Traefik sends its requests to) comes from: "user" when set in the
-- app's form, "port_env" for the PORT variable given to the container, "detected" when read from what the
-- app listens on, "expose" from its image's EXPOSE. Every port is set in the form for now.
ALTER TABLE apps ADD COLUMN port_source TEXT NOT NULL DEFAULT 'user'
    CHECK (port_source IN ('user', 'port_env', 'detected', 'expose'));

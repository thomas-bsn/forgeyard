-- The node's IP on its local network, reported by its agent: where a reverse proxy reaches its Traefik.
ALTER TABLE nodes ADD COLUMN local_ip TEXT NOT NULL DEFAULT '';

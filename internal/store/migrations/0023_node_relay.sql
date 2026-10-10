-- A node relayed by Forgeyard's own machine: visits reach that machine, whose Traefik passes this node's
-- apps to it over the local network (its Traefik listens on ingress_http_port, as behind a proxy). Its
-- apps' DNS records point to Forgeyard's machine. Until now this was guessed from a node behind a proxy
-- sharing the public IP of Forgeyard's machine: those nodes are relayed.
ALTER TABLE nodes ADD COLUMN relayed INTEGER NOT NULL DEFAULT 0;

UPDATE nodes SET relayed = 1, public_ip = ''
WHERE is_local = 0 AND ingress_mode = 'proxy'
  AND EXISTS (SELECT 1 FROM nodes WHERE is_local = 1)
  AND public_ip IN ('', (SELECT public_ip FROM nodes WHERE is_local = 1));

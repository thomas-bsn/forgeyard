-- lan: the node is on the same network as Forgeyard's machine, said when it was added. Its agent then
-- reaches the server by its local address, which the install command gives it.
ALTER TABLE nodes ADD COLUMN lan INTEGER NOT NULL DEFAULT 0;
UPDATE nodes SET lan = 1 WHERE relayed = 1 OR is_local = 1;

-- registry_sealed: the credentials of a private registry the stack's images are pulled from (encrypted JSON:
-- server, username, password). Empty for a stack of public images.
ALTER TABLE stacks ADD COLUMN registry_sealed TEXT NOT NULL DEFAULT '';

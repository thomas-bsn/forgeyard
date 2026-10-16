-- The GPUs a node's Docker can give to containers, as its agent found them (JSON array), and the
-- result of that check: ok, unsupported (no NVIDIA driver nor Container Toolkit), error; '' not checked.
ALTER TABLE nodes ADD COLUMN gpus TEXT NOT NULL DEFAULT '[]';
ALTER TABLE nodes ADD COLUMN gpu_status TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN gpu_error TEXT NOT NULL DEFAULT '';

-- The GPUs an app may use (JSON {vendor, all, ids}); '' for none. Access only: not a reservation.
ALTER TABLE apps ADD COLUMN gpu TEXT NOT NULL DEFAULT '';

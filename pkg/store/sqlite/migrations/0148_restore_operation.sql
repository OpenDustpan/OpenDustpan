-- Whether the running restore holds the maintenance mode that it entered.
-- The restore sets it in the transaction of its own state change, so the
-- next holder after a takeover knows who entered maintenance mode, and a
-- restore never leaves the maintenance mode of an operator. The restores
-- table survives the swap of a restore.
ALTER TABLE restores ADD COLUMN maintenance_entered INTEGER NOT NULL DEFAULT 0
    CHECK (maintenance_entered IN (0, 1));

-- The object texts of the audit entries of the running restore: the work
-- that its enter canceled and the live blobs that it repaired, as a JSON
-- list. The restore stores each one when it acts, so the entries survive the
-- swap, which replaces the audit log, and a takeover; it writes them after
-- the swap or at its end.
ALTER TABLE restores ADD COLUMN audit_events TEXT NOT NULL DEFAULT '[]';

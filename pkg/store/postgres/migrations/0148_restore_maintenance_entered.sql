-- Whether the running restore holds the maintenance mode that it entered.
-- The restore sets it in the transaction of its own state change, so the
-- next holder after a takeover knows who entered maintenance mode, and a
-- restore never leaves the maintenance mode of an operator. The restores
-- table survives the swap of a restore.
ALTER TABLE restores ADD COLUMN maintenance_entered BOOLEAN NOT NULL DEFAULT false;

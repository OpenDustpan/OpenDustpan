-- A manual lock on a whole namespace. The kind namespace joins unit and
-- collection, and object_id is the ID of the namespace, so that a namespace
-- gets no lock from an older namespace of the same name.
ALTER TABLE manual_locks DROP CONSTRAINT manual_locks_kind_check;

ALTER TABLE manual_locks
    ADD CONSTRAINT manual_locks_kind_check CHECK (kind IN ('unit', 'collection', 'namespace'));

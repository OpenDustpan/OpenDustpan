-- The namespace of a principal. An empty value is a principal of the whole
-- installation. A principal of a namespace is a service principal that can
-- only hold grants inside that namespace. The subject stays unique on the
-- whole installation, because a sign-in finds a principal by its subject.
ALTER TABLE principals ADD COLUMN namespace TEXT NOT NULL DEFAULT '';

CREATE INDEX principals_namespace ON principals (namespace, subject COLLATE "C") WHERE deleted_at_ms IS NULL;

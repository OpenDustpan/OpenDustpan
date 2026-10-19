-- The kind of the principal that acted (person, service or workload), and
-- the job whose token acted. The hash of an entry covers both.
ALTER TABLE audit_entries ADD COLUMN principal_kind text DEFAULT ''::text NOT NULL;
ALTER TABLE audit_entries ADD COLUMN job_id text DEFAULT ''::text NOT NULL;

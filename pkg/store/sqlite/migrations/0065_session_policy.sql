-- The session policy: the idle limit and the maximum length of a console
-- session. One row keyed by id 1 holds the administrator settings, seeded
-- with the defaults: 15 minutes with no use (PCI DSS v4.0.1 requirement
-- 8.2.8) and 12 hours in all (NIST SP 800-63B-4, AAL3). updated_at_ms is
-- the time of the last change, or 0 for the seeded defaults. The row
-- carries no fence: it describes the installation, not one namespace.
CREATE TABLE session_policy (
    id            INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
    idle_ms       INTEGER NOT NULL,
    max_length_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
) STRICT;

INSERT INTO session_policy (id, idle_ms, max_length_ms, updated_at_ms) VALUES (1, 900000, 43200000, 0);

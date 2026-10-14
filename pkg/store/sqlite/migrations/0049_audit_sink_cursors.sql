-- Audit sink cursors: for each log sink and namespace, the highest audit
-- entry sequence that the sink holds (ADR 60 decision 13). The shipper reads
-- the entries above the cursor, writes them to the sink, and only then moves
-- the cursor. An update never lowers a cursor, so two instances that ship
-- the same batch never move it back. Sink names the sink setting; a row of a
-- sink that the operator removed stays and does no harm. The table holds no
-- foreign key to namespaces, so a cursor never blocks a namespace delete.
CREATE TABLE audit_sink_cursors (
    sink          TEXT    NOT NULL,
    namespace     TEXT    NOT NULL,
    sequence      INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    PRIMARY KEY (sink, namespace)
) STRICT;

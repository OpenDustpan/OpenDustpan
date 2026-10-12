-- Sensitive marks of OpenTofu state versions (ADR 41 decisions 8 and 9).
-- The json-state rendering upload keeps only the JSON Pointer paths that
-- sensitive_values marks, by resource instance address, as JSON. It never
-- keeps a value from the rendering. The column stays NULL for versions
-- stored before the server kept marks, and for versions whose rendering
-- marked nothing: reads of those versions mask every attribute value.
ALTER TABLE state_versions ADD COLUMN sensitive_marks BLOB;

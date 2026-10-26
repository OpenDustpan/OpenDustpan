-- The size and the MD5 of the stored bytes of the blobs that a log chunk and
-- a registry release name. A backup copies a blob only when the rows name
-- these values, and checks the copy against them, as it does for a state
-- version. A plan part keeps the same two values in the document of its plan.
--
-- stored_size and stored_md5 of a chunk are those of the sealed bytes in the
-- blob store, not those of the plain chunk (size and sha256 hold those). The
-- confirm of the chunk writes them. A chunk row without them (the MD5 is the
-- empty value) is not confirmed, or was confirmed by an earlier version of the
-- server, and a backup lists it as not copied.
--
-- stored_files_json of a release holds one JSON list with the SHA-256, the
-- size and the MD5 of the checksums file, of the signature file and of each
-- archive. The empty text is a release that an earlier version of the server
-- stored.
ALTER TABLE job_log_chunks ADD COLUMN stored_size bigint NOT NULL DEFAULT 0
    CONSTRAINT job_log_chunks_stored_size_check CHECK (stored_size >= 0);
ALTER TABLE job_log_chunks ADD COLUMN stored_md5 bytea NOT NULL DEFAULT ''
    CONSTRAINT job_log_chunks_stored_md5_check CHECK (octet_length(stored_md5) IN (0, 16));
ALTER TABLE registry_releases ADD COLUMN stored_files_json text NOT NULL DEFAULT ''
    CONSTRAINT registry_releases_stored_files_check
    CHECK (octet_length(stored_files_json) <= 1048576 AND (stored_files_json = '' OR pg_input_is_valid(stored_files_json, 'jsonb')));

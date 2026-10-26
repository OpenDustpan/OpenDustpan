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
ALTER TABLE job_log_chunks ADD COLUMN stored_size INTEGER NOT NULL DEFAULT 0 CHECK (stored_size >= 0);
ALTER TABLE job_log_chunks ADD COLUMN stored_md5 BLOB NOT NULL DEFAULT X''
    CHECK (length(stored_md5) IN (0, 16));
ALTER TABLE registry_releases ADD COLUMN stored_files_json TEXT NOT NULL DEFAULT ''
    CHECK (length(CAST(stored_files_json AS BLOB)) <= 1048576 AND (stored_files_json = '' OR json_valid(stored_files_json)));

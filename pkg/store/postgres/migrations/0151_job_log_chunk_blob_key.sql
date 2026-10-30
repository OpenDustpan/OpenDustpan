-- The key of the blob of the attempt that won the chunk. Each write attempt of
-- a chunk writes its own key, job/<job ID>/log/<version ID>/<seq>-<attempt ID>,
-- and the first confirm that commits sets this column. The empty text is a
-- chunk that an earlier version wrote: its blob has the key
-- job/<job ID>/log/<version ID>/<seq>.
ALTER TABLE job_log_chunks ADD COLUMN blob_key TEXT NOT NULL DEFAULT '' CHECK (length(blob_key) <= 512);

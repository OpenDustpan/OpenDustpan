-- Checkpoint blobs: the content of each checkpoint lives in the blob store
-- under blob_key (pulumi/<stack ID>/<update ID>/<sequence>, gzipped), and
-- SQL keeps the metadata. size is the uncompressed size for history, and
-- blob_size with blob_md5 describe the stored bytes for backups. The columns
-- stay NULL for rows written before blobs. The deployment column stays for
-- those rows: reads accept a row with content or a row with a blob key. New
-- saves always write blobs and store no content.
ALTER TABLE checkpoints ADD COLUMN blob_key TEXT;
ALTER TABLE checkpoints ADD COLUMN size INTEGER;
ALTER TABLE checkpoints ADD COLUMN blob_size INTEGER;
ALTER TABLE checkpoints ADD COLUMN blob_md5 BLOB;

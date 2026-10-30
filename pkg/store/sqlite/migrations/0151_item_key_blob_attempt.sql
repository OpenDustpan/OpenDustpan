-- The attempt part of the key of the wrapped item key blob that the record
-- names. Each write of a wrapped item key uses a key with a new attempt part,
-- so two writes never share a key, and a store that replaces an object on a
-- second write never replaces a blob that a record names. The key is
-- namespaces/<namespace ID>/item-keys/<id>/v<blob_version>-<blob_attempt>.
-- The attempt part is 20 lowercase ASCII letters and digits, so it names no
-- other path. SQLite adds a NOT NULL column only with a default. The check
-- refuses the default, so each write gives the value.
ALTER TABLE item_keys ADD COLUMN blob_attempt TEXT NOT NULL DEFAULT ''
    CHECK (length(blob_attempt) = 20 AND blob_attempt NOT GLOB '*[^a-z0-9]*');

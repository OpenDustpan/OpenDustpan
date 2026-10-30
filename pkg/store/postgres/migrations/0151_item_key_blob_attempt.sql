-- The attempt part of the key of the wrapped item key blob that the record
-- names. Each write of a wrapped item key uses a key with a new attempt part,
-- so two writes never share a key, and a store that replaces an object on a
-- second write never replaces a blob that a record names. The key is
-- namespaces/<namespace ID>/item-keys/<id>/v<blob_version>-<blob_attempt>.
-- An empty value is a record of the first key layout,
-- namespaces/<namespace ID>/item-keys/<id>/v<blob_version>, which stays
-- readable. A new record always has an attempt part of 20 characters.
ALTER TABLE item_keys ADD COLUMN blob_attempt text NOT NULL DEFAULT ''
    CHECK (blob_attempt = '' OR length(blob_attempt) = 20);

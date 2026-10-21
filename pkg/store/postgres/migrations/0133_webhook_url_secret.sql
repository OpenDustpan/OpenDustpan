-- The URL of a webhook sink can hold a token, so it is a secret item of the
-- sink, like the URL of a chat sink, and the target of the sink holds only
-- the host. Until now the target held the whole URL as plain text, and every
-- backup exported it as plain text.
--
-- A migration cannot seal a value: the data key of a namespace is out of
-- reach of SQL. So this migration removes the URL and does not move it. For
-- each webhook sink whose target is a URL, it does these steps:
--   - It sets the target to the host of the URL.
--   - It puts a placeholder item first among the secret items. The
--     placeholder has an empty value, which never opens. The server then
--     refuses to send to the sink, and the answer of the sink shows that it
--     has no URL. The signing keys keep their refs and move one place down.
-- A person sets the URL of each such sink again with a replace of the sink.
-- The replace seals the new URL as a new item and drops the placeholder.
-- A target that holds user information gets an empty target, so that the
-- information does not stay in the table.
INSERT INTO notification_sink_refs (namespace_id, ref)
SELECT namespace_id, 'itm-nourl-' || id
FROM notification_sinks
WHERE kind = 'webhook' AND lower(substr(target, 1, 8)) = 'https://'
ON CONFLICT DO NOTHING;

UPDATE notification_sink_secrets
SET position = position + 1
WHERE (namespace_id, sink_id) IN (
    SELECT namespace_id, id
    FROM notification_sinks
    WHERE kind = 'webhook' AND lower(substr(target, 1, 8)) = 'https://'
);

INSERT INTO notification_sink_secrets (namespace_id, ref, sink_id, position, ciphertext)
SELECT namespace_id, 'itm-nourl-' || id, id, 0, ''::bytea
FROM notification_sinks
WHERE kind = 'webhook' AND lower(substr(target, 1, 8)) = 'https://'
ON CONFLICT DO NOTHING;

UPDATE notification_sinks
SET target = CASE
    WHEN position('@' IN split_part(split_part(split_part(substr(target, 9), '/', 1), '?', 1), '#', 1)) > 0 THEN ''
    ELSE lower(split_part(split_part(split_part(substr(target, 9), '/', 1), '?', 1), '#', 1))
END
WHERE kind = 'webhook' AND lower(substr(target, 1, 8)) = 'https://';

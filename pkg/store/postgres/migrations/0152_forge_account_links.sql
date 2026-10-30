-- Links from an account of a Git host to a principal, and the one-time requests
-- that make them. A link and a request belong to the installation, not to a
-- namespace: a person has one principal for every namespace. An account is
-- named by the kind of the Git host, the host name of its API and the stable
-- account ID that the Git host gives. The primary key compares the three
-- columns byte for byte, so IDs that differ only in case are two accounts. The
-- login is text for a person to read and is never compared. A link goes away
-- with its principal, in the same transaction as the end of the principal.
CREATE TABLE forge_account_links (
    kind         text NOT NULL CHECK (kind IN ('github', 'gitlab')),
    host         text NOT NULL,
    account_id   text NOT NULL,
    login        text NOT NULL,
    principal_id text NOT NULL REFERENCES principals (id),
    linked_at_ms bigint NOT NULL,
    PRIMARY KEY (kind, host, account_id)
);

-- The links of one principal, for the list of a person.
CREATE INDEX forge_account_links_principal ON forge_account_links (principal_id);

-- A request holds the SHA-256 of its one-time token and never the token. The
-- principal is the person who started it. The proved_* columns hold the account
-- that a signed event of the Git host named, and proved_at_ms is 0 until then.
-- used_at_ms is 0 until the person accepts. A request is good for 5 minutes from
-- created_at_ms.
CREATE TABLE forge_link_requests (
    token_sha256       text NOT NULL PRIMARY KEY CHECK (length(token_sha256) = 64),
    principal_id       text NOT NULL REFERENCES principals (id),
    created_at_ms      bigint NOT NULL,
    expires_at_ms      bigint NOT NULL,
    proved_kind        text NOT NULL,
    proved_host        text NOT NULL,
    proved_account_id  text NOT NULL,
    proved_login       text NOT NULL,
    proved_delivery_id text NOT NULL,
    proved_at_ms       bigint NOT NULL,
    used_at_ms         bigint NOT NULL,
    CHECK (expires_at_ms > created_at_ms AND proved_at_ms >= 0 AND used_at_ms >= 0)
);

-- The open and the expired requests of one principal, for the limit and the clean-up.
CREATE INDEX forge_link_requests_principal ON forge_link_requests (principal_id, expires_at_ms);

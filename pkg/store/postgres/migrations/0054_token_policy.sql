-- The token life cycle policy and the inactivity disable (ADR 53, decisions
-- 2, 4, 5 and 6). One row keyed by id 1 holds the administrator settings,
-- seeded with the defaults: a maximum token lifetime of 365 days, and an
-- inactivity period of 90 days (PCI DSS v4.0.1 requirement 8.2.6) that
-- covers access tokens and user accounts and skips the break-glass
-- credential. An inactivity period of 0 turns the disable off. The row
-- carries no fence: it describes the installation, not one namespace.
--
-- disabled_at_ms on a token and on a principal records the inactivity
-- disable. A disabled token or principal does not authenticate.
--
-- last_active_at_ms is the activity baseline of a principal: an enable sets
-- it, and the sweep counts it as activity. This migration sets it to the
-- time of the upgrade for each principal that exists, so the first sweep
-- after an upgrade does not disable every person that has no token yet.
CREATE TABLE access_token_policy (
    id                  BIGINT  NOT NULL PRIMARY KEY CHECK (id = 1),
    max_lifetime_ms     BIGINT  NOT NULL,
    inactivity_ms       BIGINT  NOT NULL,
    disable_tokens      BOOLEAN NOT NULL,
    disable_accounts    BOOLEAN NOT NULL,
    disable_break_glass BOOLEAN NOT NULL,
    updated_at_ms       BIGINT  NOT NULL
);

INSERT INTO access_token_policy
    (id, max_lifetime_ms, inactivity_ms, disable_tokens, disable_accounts, disable_break_glass, updated_at_ms)
VALUES (1, 31536000000, 7776000000, TRUE, TRUE, FALSE, 0);

ALTER TABLE access_tokens ADD COLUMN disabled_at_ms BIGINT;

ALTER TABLE principals ADD COLUMN disabled_at_ms BIGINT;

ALTER TABLE principals ADD COLUMN last_active_at_ms BIGINT NOT NULL DEFAULT 0;

UPDATE principals SET last_active_at_ms = FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000)::bigint;

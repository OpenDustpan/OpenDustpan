-- The lifetime of an access token that a workload gets in a token exchange,
-- in milliseconds. It is a setting of the token policy of the installation,
-- one hour by default.
ALTER TABLE access_token_policy ADD COLUMN exchange_lifetime_ms INTEGER NOT NULL DEFAULT 3600000;

-- The freshness of the console sign-in.
--
-- max_age_ms on a sign-in request is the max_age that the start sent to the
-- identity provider, in milliseconds. NULL means that the start sent none.
-- The callback refuses an authentication older than it.
--
-- token_freshness_ms on the session policy is how old the authentication
-- of a console session can be when the session creates or rotates an access
-- token. NULL means the default of the server. 0 turns the check off.
--
-- oidc_providers binds each provider ID of the console sign-in to the
-- issuer that it had at its first start. The subject of a person holds the
-- provider ID, not the issuer, so the server refuses to start when the
-- issuer of a provider ID changes. The rows carry no fence: they describe
-- the installation, not one namespace.
ALTER TABLE sign_in_requests ADD COLUMN max_age_ms BIGINT;

ALTER TABLE session_policy ADD COLUMN token_freshness_ms BIGINT;

CREATE TABLE oidc_providers (
    provider_id   TEXT   NOT NULL PRIMARY KEY,
    issuer        TEXT   NOT NULL,
    created_at_ms BIGINT NOT NULL
);

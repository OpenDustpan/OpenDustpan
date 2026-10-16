-- The plain OAuth 2.0 mode of the sign-in providers that the API manages.
--
-- mode is "oidc" for an OpenID provider, as every earlier row is, or
-- "oauth" for a plain OAuth 2.0 provider. A provider in the oauth mode has
-- an empty issuer: the server finds the person with a GET of userinfo_url
-- with the access token, and the value of subject_claim in the answer is
-- the identity of the person. name_claim, username_claim and email_claim
-- give the display name. The URLs and the claims are empty for a provider
-- in the oidc mode.
ALTER TABLE sign_in_providers ADD COLUMN mode TEXT NOT NULL DEFAULT 'oidc';
ALTER TABLE sign_in_providers ADD COLUMN authorization_url TEXT NOT NULL DEFAULT '';
ALTER TABLE sign_in_providers ADD COLUMN token_url TEXT NOT NULL DEFAULT '';
ALTER TABLE sign_in_providers ADD COLUMN userinfo_url TEXT NOT NULL DEFAULT '';
ALTER TABLE sign_in_providers ADD COLUMN subject_claim TEXT NOT NULL DEFAULT '';
ALTER TABLE sign_in_providers ADD COLUMN name_claim TEXT NOT NULL DEFAULT '';
ALTER TABLE sign_in_providers ADD COLUMN username_claim TEXT NOT NULL DEFAULT '';
ALTER TABLE sign_in_providers ADD COLUMN email_claim TEXT NOT NULL DEFAULT '';

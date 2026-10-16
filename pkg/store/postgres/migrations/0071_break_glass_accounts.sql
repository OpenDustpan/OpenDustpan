-- Break-glass accounts: a person that signs in without the identity
-- provider. A break-glass account is always exempt from the inactivity
-- disable. Only a person can have it. Existing principals get false.
ALTER TABLE principals ADD COLUMN break_glass BOOLEAN NOT NULL DEFAULT FALSE;

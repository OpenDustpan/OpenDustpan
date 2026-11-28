# mtls-exchange Specification

## Purpose
Define how a workload exchanges its client certificate for a short-lived trust token over direct TLS. The server accepts only a chain that leads to the active version of the trust bundle of the namespace and matches exactly one rule by exact SPIFFE ID. It stores the token of the kind `mtls-exchange` and its audit entry in one transaction, checks again at the record step that the bundle version is still active, and refuses a wildcard in a SPIFFE ID pattern.

## Requirements

### Requirement: Verify chain and SPIFFE ID

When a workload calls the mTLS exchange over direct TLS, the server SHALL accept only a verified chain that matches exactly one enabled rule.

#### Scenario: Valid chain
- **WHEN** A leaf with a SPIFFE ID in the URI SAN that one enabled rule names chains to the active version of the bundle of the namespace.
- **THEN** The server issues a trust token and writes an audit entry.

#### Scenario: Wrong CA
- **WHEN** The chain does not lead to the active version of the bundle of the namespace.
- **THEN** The server refuses with 401 and no token.

#### Scenario: Expired leaf
- **WHEN** The leaf is past its not-after time.
- **THEN** The server refuses with 401 and no token.

#### Scenario: Revoked or unknown
- **WHEN** The revocation source reports revoked, or is unreachable.
- **THEN** The server refuses with 401 and no token.

#### Scenario: No match
- **WHEN** No enabled rule names the SPIFFE ID, or two rules do.
- **THEN** The server refuses with 401 and no token.

### Requirement: Issue stored trust token

When the checks pass, the server SHALL issue a stored access token of the kind `mtls-exchange` with the exchanged-token lifetime.

#### Scenario: Kind of the token
- **WHEN** The checks pass.
- **THEN** The token has the kind `mtls-exchange`, the token list of the principal shows that kind, the issue audit entry records it, and a request with the token has the credential `trust-token`.

#### Scenario: Capped expiry
- **WHEN** The checks pass with the default setting.
- **THEN** The expiry is at most 1 hour from issue and at most the leaf not-after time.

### Requirement: Atomic issue

When the server issues a token, the server SHALL commit the token with its audit entry in one transaction.

#### Scenario: Store failure
- **WHEN** The transaction fails.
- **THEN** No token and no issue audit entry exist, and the answer is 500.

### Requirement: End use

If a trust token is revoked or its principal is disabled, then the server SHALL deny its later use.

#### Scenario: Revoke
- **WHEN** A holder revokes the token.
- **THEN** The next use gives 401.

### Requirement: Recheck the bundle version at the record step

When the server stores an mTLS trust token, the server SHALL check in the same transaction that the bundle version that verified the chain is still the active version of the bundle of the rule in the namespace.

#### Scenario: Version still active
- **WHEN** The active version of the bundle is the version that verified the chain.
- **THEN** The server stores the token and its audit entry.

#### Scenario: Bundle rotated after the verify step
- **WHEN** A rotation made another version active after the chain was verified and before the token is stored.
- **THEN** The server refuses with 401 and no token and no issue audit entry exist.

#### Scenario: Version not stored
- **WHEN** The version that verified the chain is no longer stored in the bundle.
- **THEN** The server refuses with 401 and no token and no issue audit entry exist.

#### Scenario: Bundle of another namespace
- **WHEN** The bundle or the version is the active bundle or version of another namespace.
- **THEN** The server refuses with 401 and no token and no issue audit entry exist.

### Requirement: Exact SPIFFE ID in a rule

When a caller creates or updates a trust rule with a `spiffePattern`, the server SHALL refuse a pattern that has a `*` and name the field and the need for an exact SPIFFE ID in the answer.

#### Scenario: Exact SPIFFE ID
- **WHEN** The rule has a `spiffePattern` with no `*`.
- **THEN** The server stores the rule as before.

#### Scenario: Wildcard at create
- **WHEN** A create names a `spiffePattern` that ends in `/*`.
- **THEN** The server refuses with 400 and the error code trust-rule.spiffe-wildcard, the message names `spiffePattern` and says that the rule must name an exact SPIFFE ID, and the server stores nothing and writes no audit entry.

#### Scenario: Wildcard at update
- **WHEN** An update sets `spiffePattern` to a value that ends in `/*`.
- **THEN** The server refuses with 400 and the error code trust-rule.spiffe-wildcard, and the stored rule and its revision stay as they were.

#### Scenario: Rule with no SPIFFE ID
- **WHEN** A rule has an empty `spiffePattern`.
- **THEN** The server handles the rule as before, and a JWT exchange of the rule is unchanged.

### Requirement: The recheck reads only the rules that name the SPIFFE ID

The store SHALL check in the transaction of an mTLS exchange that exactly one rule of the bundle names the SPIFFE ID, and that this rule is the rule of the exchange. It SHALL read at most 2 rules for this check: those of the namespace with the same bundle ID and SPIFFE ID pattern. The number of rows that it reads SHALL NOT depend on the number of rules of the namespace.

#### Scenario: A namespace with many rules

- **WHEN** a namespace holds 1,000 rules with other SPIFFE IDs and one rule that names the SPIFFE ID of the exchange, and the store records the exchange
- **THEN** the store records the token, and the recheck reads at most 2 rows for the match and 1 row for the bundle

#### Scenario: Two rules of one bundle name the SPIFFE ID

- **WHEN** a second rule of the same bundle names the SPIFFE ID after the service read its rules
- **THEN** the store refuses with `trustrule.ErrNotFound`, stores no token and writes no exchange audit entry

#### Scenario: The same SPIFFE ID in another bundle

- **WHEN** a rule of another bundle of the same namespace names the same SPIFFE ID
- **THEN** that rule does not count, and the store records the exchange for the rule of the bundle of the exchange

#### Scenario: The same SPIFFE ID and bundle ID in another namespace

- **WHEN** a rule of another namespace has the same bundle ID text and the same SPIFFE ID pattern
- **THEN** the recheck does not read it and does not count it

#### Scenario: The one matching rule is not the rule of the exchange

- **WHEN** the exchange names the rule R1 and only the rule R2 of the same bundle names the SPIFFE ID
- **THEN** the store refuses with `trustrule.ErrNotFound`

#### Scenario: A pattern that only looks like the SPIFFE ID

- **WHEN** a stored pattern differs from the SPIFFE ID by case, by a trailing slash, by a trailing space, or holds `%`, `_` or `*` at the place of a segment
- **THEN** that rule does not count as a match, because the compare is an exact equality of the whole text

#### Scenario: The rule has no bundle

- **WHEN** the rule of the exchange has no bundle ID
- **THEN** the store refuses with `trustrule.ErrNotFound` and reads no other rule

### Requirement: The match reads by an index

The store SHALL read the rules that name a SPIFFE ID by the namespace, the bundle ID and the SPIFFE ID pattern together, through an index on those three columns in this order, in the PostgreSQL adapter and in the SQLite adapter. The read SHALL use no sort and SHALL read no more than 2 rows.

#### Scenario: The plan of the read

- **WHEN** the store reads the rules that name a SPIFFE ID in a table of 20,000 rules of one namespace
- **THEN** the plan of the query searches `trust_rules_spiffe` with all three columns, holds no sort and no scan of the table, in both adapters

### Requirement: The answers of the recheck stay the same

The change SHALL NOT change an answer of the exchange. Each refusal of the recheck SHALL keep its error sentinel (`trustrule.ErrNotFound` for the match and the bundle, `trustrule.ErrInvalid` for a kind that no exchange makes), and the route SHALL keep its HTTP status and its error code. The text of the refusal for two matching rules MAY say "at least 2 rules" in place of the exact count.

#### Scenario: The conformance cases of the exchange

- **WHEN** the conformance suite of the trust rule store runs on the SQLite adapter and on the PostgreSQL adapter
- **THEN** every case of the mTLS exchange that passed before this change passes, with no change to its expected sentinel

#### Scenario: A refusal of the store reaches the caller as before

- **WHEN** the store refuses a recorded exchange with `trustrule.ErrNotFound`
- **THEN** the route answers 401 `mtls.no-matching-rule`, the body holds no token, and the audit log holds one denial entry

### Requirement: The check does not trust the read

The match check SHALL compare the bundle ID and the SPIFFE ID pattern of each row that it receives with the bundle ID of the rule of the exchange and the SPIFFE ID of the exchange, and SHALL refuse unless the rule row has the ID of the rule of the exchange. It SHALL not rely on the query, the index or the driver to have filtered the rows.

#### Scenario: A row that the query would not return

- **WHEN** a test sends the check a row of another bundle, a row with a wildcard pattern, or a rule row with another ID, as if the query had failed
- **THEN** the check does not count the row as a match, or refuses with `trustrule.ErrNotFound`

### Requirement: No new migration and no compatibility code

The change SHALL add the index to the baseline file of each dialect in place, in name order, and SHALL NOT add a numbered migration, a conversion or a compatibility read for a rule that was stored with a wildcard pattern.

#### Scenario: An existing dev database

- **WHEN** the operator starts the new binary on a dev database that was made from the old baseline
- **THEN** the store refuses it with `ErrSchemaDiverged`, and `opendustpan dev reset` makes the schema again

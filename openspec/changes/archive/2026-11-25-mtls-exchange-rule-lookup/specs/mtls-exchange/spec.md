## ADDED Requirements

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

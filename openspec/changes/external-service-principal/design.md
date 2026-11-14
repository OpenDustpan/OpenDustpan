## Context

ADR 369 decisions 3 to 5 add external service principals, classification rules (a claim condition, an LDAP group or OU, or a glob on the external username) and the field `existingPrincipals`. Main has the kind `service` only for principals that the API makes with the source `local` (`pkg/principal/namespace.go:47`), no external ID apart from the subject, no owner and no classification. A wrong classification gives a machine a console session, or a person the rights of a machine, so this is security code (ADR 250).

## Decisions

1. **One classification type.** `principal.Classification` has unexported fields: the kind (`service`), the ID of the rule, the time and the actor. Only `principal.Classify(rule, account)` makes one. The zero value is "not classified", which keeps the principal as it is. A principal record holds at most one.
2. **One rule type with three matchers.** `principal.ClassificationRule` has unexported fields and one constructor `NewClassificationRule(scope, matcher, owner)`. The matcher is one of: a claim condition (reuses the checks of `trustrule.ValidateConditions`), an LDAP group or OU (by its stable ID), or a glob.
   - A glob holds 1 to 64 bytes of ASCII letters, digits, `-`, `_`, `.`, `@` and `*`. It needs at least one byte that is not `*`, so `*`, `**` and `***` are refused. The match compares exact bytes, with no case folding and no Unicode normalization.
3. **The external key.** The principal record gets `ExternalID` and `ExternalSource` (for example `gitlab:<host>` or `ldap:<directory id>`). The pair is unique. A lookup of an external account uses the pair, never the name. A rename changes only the display name and the matched name.
4. **The direction of a rename.** A rename into a glob of a live rule classifies the principal, with an audit entry. A rename out of it changes nothing. Only `DELETE /api/dustpan/v1/principals/{principal}/classification` with the action of the rules and a reason makes it a person again.
5. **`existingPrincipals`.** The service computes the set of existing principals that the rule would classify. The set is the sorted list of principal IDs. The digest is the SHA-256 of the list, one ID per line. In one transaction, the service:
   - writes the rule;
   - for `refuse` (or a missing value) with a non-empty set, refuses with 409 `principal.reclassify-refused`, the count and the preview route;
   - for `skip`, classifies nothing and answers the set;
   - for `confirm`, compares the digest of the current set with `expectedDigest`, and refuses with 409 `principal.reclassify-stale` on a difference;
   - for `apply`, classifies the current set;
   - writes one audit entry with the rule change, the value, the set and its count.

   A value that is not one of the four is refused with 400 before the transaction. The preview runs the same computation in a read-only transaction.
6. **The kind at the exchange.** The exchange of `internal/trustrulesvc` gives the kind of actor of ADR 369 decision 3. A token whose event source is automated (a schedule, an API trigger, a pipeline that another pipeline started, a token with no user) never acts as a person, also when a rule would name a person. The list of automated values for each issuer kind is in the code rules catalog. An unknown value counts as automated (fail closed).
7. **An active account at each exchange.** The exchange reads the account in its source by its external ID. An inactive account, a read that fails or an answer that the server cannot read gives no token.

## Structure

- `Classification` and `ClassificationRule` with unexported fields and checked constructors: a glob of `*` alone, a rule with no scope, and a classification with no rule cannot exist.
- The kind of a principal changes only through the classification service, so a person record cannot become a service principal by a plain update.
- No custom analyzer and no new tool (ADR 364).

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| Rule constructor (`pkg/principal`) | Glob grammar, at least one byte that is not `*`, exact bytes, claim checks reused. | `TestClassificationRuleHostile`: `*`, `**`, a glob with a newline, a 65-byte glob, a lookalike Cyrillic letter, `System-*` against `system-x`, a claim condition on `iss`. Each is refused or does not match. |
| Classification service (NEW package `internal/principalsvc`) | The four values, the digest compare, the set in one transaction, the rename direction. | `TestReclassifyModes`: no value with matches (409), `skip`, `confirm` with the right digest, `confirm` with a stale digest (409, nothing changes), `confirm` with the digest of another set (409), `apply`, an unknown value (400), a rename out of the pattern (stays service), a rename into it (becomes service), a person matched by mistake (loses the console session). |
| Exchange (`internal/trustrulesvc`) | The automated sources never act as a person; the account must be active. | `TestExchangeAutomatedNeverPerson`: a schedule token of a person, an API trigger, a pipeline started by a pipeline, a token with no user, an unknown event source, a rule that names a person for a schedule. `TestExchangeInactiveAccount`: an inactive account, a source that does not answer, a malformed answer. Each gives no token or a non-person actor. |
| Sign-in (`internal/signinsvc`) and HTTP handler | A principal of kind `service` gets no console session, MFA or break-glass. | `TestServicePrincipalNoSession`: a sign-in, an MFA enroll and a break-glass mark of a service principal. Each is refused with its code and an audit entry. |

## Risks

- A glob that matches a person by mistake removes the console access of that person. This is the safe direction, and the preview shows it before the save.
- A source that is down blocks the exchanges of its accounts. This fails closed.

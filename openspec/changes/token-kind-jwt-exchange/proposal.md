## Problem

A site administrator lists the access tokens of a service principal. A token
that the JWT exchange issued has the kind `trust`. The name says that a trust
rule issued the token. It does not say how the workload proved its identity.
When the mTLS exchange comes, a token of a certificate and a token of a JWT
must not share one kind. ADR 371 (owner, 2026-11-14) renames the kind to
`jwt-exchange`.

## Evidence

- `pkg/accesstoken/accesstoken.go:75` — `KindTrust Kind = "trust"`.
- `pkg/accesstoken/trust.go:42` — `NewTrustToken` sets `kind: KindTrust`.
- `pkg/accesstoken/service.go:693` — `credentialOf` gives `CredentialTrustToken` only when `k == KindTrust`.
- `pkg/accesstoken/scope.go:167` and `:172` — `authorizeRevoke` lists `KindService` and `KindTrust` by name.
- `pkg/store/internal/trustrulestore/trustrulestore.go:206` — the store refuses an exchange token whose kind is not `KindTrust`.
- `pkg/trustrule/trustruletest/exchange_cases.go:36` and `:93` — the conformance suite expects `KindTrust`.
- Tests: `pkg/accesstoken/trust_token_test.go:69` (`TestTrustTokenLivesTheExchangeLifetime`), `internal/trustrulesvc/exchange_test.go:131` and `:340`.
- Docs: `docs/guides/workload-identity.md:170` ("kind `trust`") and `GLOSSARY.md:630` ("the kind `trust`").
- No SQL constraint names the kind: `rg -n "'trust'" pkg/store/sqlite/migrations pkg/store/postgres/migrations` finds nothing.

## Fix boundary

**IN**

- `pkg/accesstoken/accesstoken.go`: replace `KindTrust` with `KindJWTExchange Kind = "jwt-exchange"`. Add the unexported set of exchange kinds and the method `Kind.FromExchange() bool`, true for each kind that a token exchange issues. Only this method names the exchange kinds.
- `pkg/accesstoken/service.go` (`credentialOf`) and `pkg/accesstoken/scope.go` (`authorizeRevoke`): use `FromExchange` in place of the name of one kind, so that the mTLS kind of the change `mtls-mtls-exchange-token` gets the credential `trust-token` and the revoke rule of the service tokens with no second edit.
- `pkg/accesstoken/trust.go`, `pkg/store/internal/trustrulestore/trustrulestore.go`, `pkg/trustrule/trustruletest/exchange_cases.go` and the two test files: the new constant.
- `docs/guides/workload-identity.md` and `GLOSSARY.md`: the kind `jwt-exchange`.

**OUT**

- The kind `mtls-exchange`. The change `mtls-mtls-exchange-token` adds it to the set.
- The credential `trust-token`. It stays (ADR 371, "Not decided here").
- Migration, alias or conversion code for `trust` (ADR 322, ADR 371 decision 5).

## Check

- **Guard: the JWT exchange issues `jwt-exchange`.** Test `TestTrustTokenLivesTheExchangeLifetime` in `pkg/accesstoken` (its kind check at line 69) and `testExchangeStores` in `pkg/trustrule/trustruletest`. Mutation: give the token the kind `service`; the cases fail.
- **Guard: an exchange kind gives the credential `trust-token`.** NEW table test `TestCredentialOfEachKind` in `pkg/accesstoken`, one row for each kind. Mutation: return `CredentialAccessToken` for every kind; the `jwt-exchange` row fails.
- **Guard: the revoke rule covers each exchange kind.** NEW table test `TestRevokeScopeOfEachKind` in `pkg/accesstoken`. Mutation: remove the `FromExchange` term from `authorizeRevoke`; the `jwt-exchange` row gets `ErrWrongKind`.
- **Guard: no code names `trust` as a kind.** `rg -n 'KindTrust|Kind = "trust"' --type go` finds nothing.

## ADR check

**ADR 371 decisions 1, 3 and 5 (owner).** "The stored token that the exchange issues for a JWT of a workload has the kind `jwt-exchange`. This name replaces `trust`." "No two ways to prove identity share one kind." "No alias, conversion or upgrade code reads the kind `trust`." Changed by: none. Current rule: this ADR. The change does what these decisions say. No conflict.

**ADR 284 decisions 1 and 2 (owner).** "the server issues a stored access token of ADR 6 of its own kind, `trust`, for the principal of the rule." "the access-token authenticator checks the token and gives the credential `trust-token`." Changed by: ADR 371 changes decision 1. Current rule: this ADR, with ADR 371. Decision 1 is followed through ADR 371. Decision 2 keeps the credential. No conflict.

**ADR 226 decision 4 (not owner-tagged).** The `principal` fact has "`credential` (`session`, `access-token`, `job-token` or `trust-token`)". The credential does not change. No conflict.

**ADR 322 (owner).** No migration or compatibility code before the first tag. The kind is text with no constraint; the rename edits code and docs in place. No conflict.

**ADR 364.** An unexported set with one method, not a custom check. No conflict.

## Open questions

none

Questions considered and answered:

- Which name? ADR 371 decision 1 names `jwt-exchange`.
- Does the credential change? No. ADR 371 "Not decided here" keeps `trust-token`.
- Does a stored row need a conversion? No. No installation exists (ADR 322), and `opendustpan dev reset` clears a dev database.

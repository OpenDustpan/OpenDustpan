# 371. Each way to prove identity in the token exchange has its own token kind

Status: accepted · 2026-11-14. Decided by the project owner

Changes:

- Changes ADR 284 decision 1: the kind `trust` becomes `jwt-exchange`, and an mTLS exchange issues a stored token of the kind `mtls-exchange`. This decides the point of "Not decided here" of ADR 284.

## Context

ADR 284 decision 1 (owner, 2026-10-30) says that the token exchange issues
"a stored access token of ADR 6 of its own kind, `trust`, for the principal
of the rule". The exchange of ADR 284 checks the JWT of a workload. ADR 284
leaves one point open in "Not decided here": "Whether an mTLS exchange that
issues a stored token follows decision 1 too."

ADR 54 decision 4 lets a workload prove its identity with a TLS client
certificate. The OpenSpec change `mtls-mtls-exchange-token` specifies an
exchange that takes a client certificate and issues a stored token. It was
not spec-ready, because ADR 284 decision 1 names the JWT exchange only.

A check of main on 14 November 2026 found:

- The kinds of access token are `personal`, `service` and `trust`
  (`pkg/accesstoken/accesstoken.go`, `Kind`). The name `trust` says that a
  trust rule issued the token. It does not say how the workload proved its
  identity.
- Other kinds of the server use lowercase words with hyphens, for example
  the import sources `tfe-api` and `pulumi-service`
  (`internal/unitimport/unitimport.go`) and the removal source
  `identity-provider` (`pkg/principal/removal.go`).

On 14 November 2026 the project owner decided that the token exchange gets a
stored token kind for each way to prove identity. The reason: each issued
token says how the server made it, so that a list, a revoke and an audit can tell
the tokens apart.

## Decision

1. **The JWT exchange issues the kind `jwt-exchange` (owner, 2026-11-14).**
   The stored token that the exchange issues for a JWT of a workload has the
   kind `jwt-exchange`. This name replaces `trust`. All else in ADR 284
   decision 1 stays: the token belongs to the principal of the rule, it has
   an ID, an audit entry for its issue, a lifetime from the token policy and
   a revoke through the routes of the service tokens.
2. **The mTLS exchange issues the kind `mtls-exchange` (owner,
   2026-11-14).** An exchange that checks a TLS client certificate against
   the trust rules issues a stored access token of the kind `mtls-exchange`
   for the principal of the rule. It has the same life cycle as a token of
   decision 1: an ID, an audit entry for its issue, a lifetime from the token
   policy and a revoke through the routes of the service tokens.
3. **A new way to prove identity gets a new kind (owner, 2026-11-14).** A
   later exchange that takes another proof issues a stored token of its own
   kind. No two ways to prove identity share one kind.
4. **The kind is in the API, the list and the audit entry.** The list of the
   tokens of a principal shows the kind of each token. The audit entry of
   each issue records the kind.
5. **No migration.** No installation exists, and no tag shipped the kind
   `trust` (ADR 322). The baseline schema, the code, the guides and the
   glossary change in place. No alias, conversion or upgrade code reads the
   kind `trust`.

## Not decided here

- The credential of a request. ADR 284 decision 2 and ADR 226 decision 4 give
  the credential `trust-token` to a token that a trust rule issued. This ADR
  changes no credential: a token of either kind of decision 1 or 2 gives the
  credential `trust-token`. A gate that must tell a JWT from a certificate
  needs a new decision.
- The routes, the error codes and the checks of the certificate of the mTLS
  exchange. The OpenSpec change `mtls-mtls-exchange-token` specifies them.

## Consequences

- An administrator can list, revoke and audit the tokens of a JWT exchange
  and of an mTLS exchange apart, with no read of the trust rule.
- The OpenSpec change `mtls-mtls-exchange-token` is no longer blocked by ADR
  284 decision 1.
- The constant `KindTrust`, the baseline schema, the guide of workload
  identity and the glossary entry of the token exchange change in the change
  that implements decision 1.

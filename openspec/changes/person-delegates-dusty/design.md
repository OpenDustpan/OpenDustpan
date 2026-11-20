## Context

A delegation is a stored record. It lets one person use one agent principal for a time, with a mode and a scope (ADR 354, RFC 121 decisions 11 to 17). This change writes, reads, lists, renews and ends the record. It starts no run and enforces no tool call. The agent of the first delegation is Dusty. Dusty exists only after a model connect change, which is a dependency to be written (see the proposal).

Several points have no ADR or RFC line that settles them. They are the open questions of the proposal. This design states the structure that holds for every answer, and it marks each place where an answer changes the design. Nothing below decides an open question.

## Decisions

1. **One record, stable IDs only.** The record holds the stable ID of the person, the stable ID of the agent and the stable IDs of the namespaces (ADR 362). It never holds the name `dusty`. Times are Unix milliseconds in `BIGINT`. The record holds the expanded list of actions, not the mode name alone, so a later change of a mode never widens an existing delegation (RFC 121 decision 13).
2. **The creator is the person.** The record has one field for the person. The creator is not a second field that a caller can set to another value. A service principal, a workload and an agent never delegate (RFC 121 decision 11).
3. **Console session only.** The create, renew and widen routes accept a console session only. They refuse an access token and a break-glass session (RFC 121 decision 14). A request with the delegated credential of an agent is refused by the same rule, and by the class `never` of RFC 121 decision 20 (e) when that slice lands. Whether the fresh step also applies depends on open question 7.
4. **One place decides if a delegation is live.** The function `Resolve` takes an ID and a time. It returns the live delegation or a sentinel with the cause of the end. It reads the end time, the expiry, the state of the agent and the state of the person. No other code reads the expiry or the end fields to decide liveness. This is the "next resolve" of RFC 121 decision 16. The change `revoke-stops-work` and the later enforcement call it.
5. **An end is written once, with its cause.** The end fields are set by one conditional update that applies only when they are empty. A second revoke is an idempotent success and writes no second entry. The entry is written in the same transaction as the update (a derived fact is written only inside the transaction of the write it derives from). Which writer covers an end that no request causes is open question 5.
6. **Renewal keeps the ID.** Renew updates the expiry and the last renewal time of the same row, with the same or a narrower scope, and writes an entry in the same transaction. The rules for the maximum and for an expired row are open question 4.
7. **Lifetimes are a setting.** The default and the maximum come from one read of the effective setting, and the API shows the effective values (ADR 354 decision 3). The code holds no lifetime number except the shipped default of the setting: 30 days and 90 days for the client type `server` (ADR 354 decision 3). The shape of the setting is open question 3.
8. **The own-row routes and the administrator routes share one service.** The routes differ in the authorization action and in the filter. The service takes the caller and a flag that the route sets after its action check. The action of the own-row routes is open question 1.
9. **No hidden rule.** Each default shows its effective value in the API. Each create, renew and end writes an audit entry. The list of ends is in the stored cause, not only in code.

## Structure

- The package `pkg/delegation` holds the unexported type `Delegation` with a constructor `New(args)`. Only the constructor builds a live value. It checks that the person has the kind `person`, that the agent has the kind `agent`, that the mode is known, that the list of actions is not empty, that the expiry is after the created time and not after the maximum, and that the client authentication is known. A row that the store reads is built by the same constructor, so a hand-edited row fails closed.
- The type `Caller` of the service is built from a console session only, through the constructor `FromSession`. It carries the break-glass flag. A request with another credential has no way to build a `Caller` that can create. This removes the class of bug "a new route forgot the session check".
- The domain rule of `.golangci.yml` lists `pkg/delegation`: it imports no `net/http`, no `database/sql` and no store. The service is in `internal/delegationsvc`. The port is in `pkg/delegation`. The conformance suite is `pkg/delegation/delegationtest`. Each store adapter runs it.
- No new lint rule and no custom check (ADR 364). The visibility of the fields is the guard.

## Contract

Names are proposals. Open questions 1, 3 and 6 can change the action, the settings path and the audit shape.

Port, in `pkg/delegation`:

```go
type Store interface {
    Create(ctx context.Context, d Delegation, entry audit.Entry) error
    Get(ctx context.Context, id string) (Delegation, error)
    List(ctx context.Context, filter Filter) ([]Delegation, error)
    Renew(ctx context.Context, id string, expiry time.Time, scope Scope, entry audit.Entry) error
    End(ctx context.Context, id string, cause Cause, at time.Time, entry audit.Entry) error
}
```

Sentinel errors, wrapped with `%w`: `ErrNotFound`, `ErrInvalid`, `ErrEnded` (with the cause), `ErrSessionRequired`, `ErrExpiryOutOfRange`, `ErrUnknownAgent`, `ErrUnknownMode`.

Routes (the names of the actions are open question 1; the settings path is open question 3):

| Method and path | Request | Answer | Errors |
| --- | --- | --- | --- |
| `POST /api/dustpan/v1/delegations` | `{"agent": "<principal ID>", "mode": "propose", "objects": {"namespaces": ["<ID>"], "all": false, "installation": false}, "expiresAt": "<RFC 3339>"}` | 201 with the record | 403 `delegation.session-required`, 403 `mfa.fresh-step-required`, 422 `delegation.invalid-expiry`, 422 `delegation.invalid-mode`, 422 `delegation.unknown-agent`, 422 `delegation.unknown-namespace` |
| `GET /api/dustpan/v1/delegations` | none | 200 with the delegations of the caller, each with its live state and the end cause | none |
| `GET /api/dustpan/v1/delegations/{id}` | none | 200 with the record | 404 `delegation.not-found` |
| `POST /api/dustpan/v1/delegations/{id}/renew` | `{"expiresAt": "<RFC 3339>", "objects": {...}}` | 200 with the record | 403 `delegation.session-required`, 403 `mfa.fresh-step-required`, 404 `delegation.not-found`, 422 `delegation.invalid-expiry`, 409 `delegation.ended` (open question 4) |
| `POST /api/dustpan/v1/delegations/{id}/revoke` | `{"reason": "<text>"}` (optional) | 200 with the record, also on repeat | 404 `delegation.not-found` |
| `GET /api/server/delegations` | none | 200 with every delegation | 403 `access.denied` |
| `POST /api/server/delegations/{id}/revoke` | as above | 200 | 403 `access.denied`, 404 `delegation.not-found` |
| `GET /api/dustpan/v1/delegations/settings` | none | 200 with the default and the maximum of each client type, and where each value comes from | none |

The error codes are NEW, except `mfa.fresh-step-required` and `access.denied`. Each NEW code gets its line in `internal/errcode/testdata/catalog/delegation.txt` and its entry in `internal/errcode`. These routes are tool-neutral, so they follow the error envelope of the other tool-neutral routes of `internal/api/server`, not the envelope of the Pulumi Cloud-compatible API.

Stored schema. One table for each dialect in the baseline, in the name order of the baseline (ADR 322). Columns of the table `delegation`:

| Column | Type | Note |
| --- | --- | --- |
| `id` | `TEXT` primary key | stable ID |
| `person_id` | `TEXT` not null | stable ID of the person principal |
| `agent_id` | `TEXT` not null | stable ID of the agent principal |
| `mode` | `TEXT` not null | `read`, `propose` or `operate` |
| `all_namespaces` | boolean | default false |
| `installation` | boolean | default false |
| `actions` | `TEXT` not null | the expanded list, one catalog action for each element |
| `audiences` | `TEXT` not null | empty list for Dusty |
| `client_auth` | `TEXT` not null | `public`, `confidential` or `server` |
| `created_at` | `BIGINT` not null | Unix milliseconds |
| `expires_at` | `BIGINT` not null | Unix milliseconds |
| `renewed_at` | `BIGINT` | null until the first renewal |
| `ended_at` | `BIGINT` | null while live |
| `end_cause` | `TEXT` | null while live |

A second table `delegation_namespace` holds one row for each namespace ID of a delegation, with the key `(delegation_id, namespace_id)`. The store adds an index on `person_id` and on `agent_id`. Whether the actions stay in one column or move to a second table depends on the conformance suite: it must prove the order and the exact content on both dialects. This is an implementation choice and needs no decision.

## Risks

- A stolen console session creates a delegation. The bound is the lifetime, the list, the revoke and the audit entry. With `freshAuthEnabled` off by default (ADR 385), there is no second factor on create unless open question 7 is answered with option (b).
- A temporary administrator grant widens the subject part of a live delegation for its lifetime, by design of the intersection (RFC 121 decision 14). The docs and the consent page say so.
- The audit constructor refuses a block with only `delegationID` (`pkg/audit/audit.go:256`). Open question 6 decides if the change of the constructor is part of this change.
- A renewed delegation keeps its ID, so a long chain of renewals can hide a change of scope. The rule "same or narrower" and one entry for each renewal limit it. The maximum count is open question 4.
- Reason text and agent text are data. A delegation row carries no secret and no token.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP adapter | The create, renew and widen routes accept a console session only and refuse a break-glass session. The handler checks the mode against the allowlist, the namespace ID format, the expiry against the effective maximum and the agent ID format. It takes the person from the session, never from the body. | Send create with an access token, with a break-glass session, with a body that names another person, with mode `admin`, with an expiry 91 days out, and with `installation: true` where the policy refuses it. Each is refused and stores no row. |
| service | The `Caller` is built from a console session only. The service checks that the agent is a principal of the kind `agent` that is not disabled, that the person is the caller, and the scope as open question 2 says. It reads the effective lifetimes at each call. | Call the service with a `Caller` that has the break-glass flag, with a person principal as the agent, with a disabled agent and with an expiry above the effective maximum. Each is refused with its sentinel and stores no row. |
| store | The store builds the row with `delegation.New` again. It checks in the same transaction as the insert that the agent has the kind `agent` and that the person has the kind `person`. The key of the ID and the end update that applies only to an empty end make a repeat idempotent. | Insert at the store a row with a creator that is not the person, an agent of the kind `person`, and an expiry above the maximum. Each is refused. End a row twice and check that the first cause stays and that one entry exists. |
| audit | The entry is written in the transaction of the write. A failed append fails the write. | Make the append fail on create, renew and end, and check that the row did not change. |
| read paths | An own-row route filters on the person in the query, not after the read. A row of another person answers 404 and does not say that it exists. | Read, renew and revoke the row of another person as a plain member. Each answers 404 and changes nothing. |

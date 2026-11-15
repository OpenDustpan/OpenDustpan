---
version: 5
---

## Why

**Persona: site administrator.** ADR 354 (owner) gives the kind of a principal the value `agent` and makes an unknown kind fail closed. On main, the kind has three values, and some readers of the kind would treat a fourth value in ways that nobody chose. Before an agent principal exists (person-delegates-dusty builds the first one), every reader of the kind must know `agent` or refuse it.

The user path this change must make work:

1. **API.** A site administrator lists the principals through `GET /api/server/principals` with a session credential and filters by kind; the kind `agent` is a known filter value, and an unknown value answers 400 with `principal.invalid`.
2. **API.** A create of a principal of the kind `agent` through `POST /api/server/principals` answers 400 with `principal.invalid`: an agent record needs its sponsors and is made by the model connect request (RFC 121 decisions 4 and 7), not by this route.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Create with the kind `agent` or an unknown kind | 400 `principal.invalid`: the kind is not one that this route makes. | `GET /api/server/principals?kind=agent` |

## What Changes

- `principal.Kind` and `identity.Kind` get the value `agent` (`KindAgent`): `principal.ParseKind` (pkg/principal/principal.go:96) and `identity.Kinds` (pkg/identity/identity.go:93) know it, and `audit.Entry.validatePrincipal` (pkg/audit/audit.go:327) accepts the principal kind `agent` in an entry.
- Each switch over the kind on main handles `agent` explicitly, or refuses it in its default (fail closed): internal/subscriptionsvc/subscriptionsvc.go:382, internal/api/registry/handlers.go:163, internal/api/inventory/views.go:231, pkg/notify/sinks/sinks.go:691, and pkg/accesstoken/accesstoken.go:82 (`KindFor` already refuses an unknown kind with `ErrNoTokenForKind`; an agent gets no access token of these kinds).
- The create route of principals (internal/api/server/principals.go:671) keeps making person, service and workload only, and refuses `agent` with `principal.invalid`.
- No new table, column or error code: `principals.kind` is `text NOT NULL` on main, and no tag shipped a row of another shape.

Out of scope:

- The agent block of an audit entry: ADR 380 (owner) fixes it, and audit-agent-block owns it.
- The agent key of a job token: job tokens and their mint are not on main; add-job-session-paths-runner-token-issuer owns them.
- The agent record itself (sponsors, ceiling, delegations): person-delegates-dusty and later changes.

## What main already does

- `principal.Kind` has three values and `ParseKind` refuses others with `ErrInvalid` (`pkg/principal/principal.go:96 (func ParseKind)`)
- The record holds the kind, `text NOT NULL` (`pkg/store/postgres/migrations/0168_baseline.sql:978`, the table `principals`).
- The audit entry refuses an unknown principal kind (`pkg/audit/audit.go:327 (func Entry.validatePrincipal)`)
- An access token refuses an unknown kind (`pkg/accesstoken/accesstoken.go:80 (func KindFor)`)

## ADR check

**ADR 354: Agent principals, delegation and the kind agent** (owner-tagged, all decisions).

- Quote, Changes: "Changes ADR 240 decisions 1, 3 and 4: `principal.kind` gets the value `agent`, and an unknown kind fails closed."
- Quote, decision 7: "A token from a trust rule gives `workload`, as ADR 240 decision 3 says." So `workload` stays a kind.
- Quote, decision 6: "Decisions 1 to 52 of the \"Proposal\" of the RFC apply." RFC 121 decision 4: "The record holds 1 to 5 sponsors (persons) and a fallback group."; decision 7: "The model connect request creates it, with the connecting administrator as its creator and first sponsor."
- Effect: the change adds the kind and makes every reader know it or refuse it; it makes no agent record, so the create route refuses `agent`.
- Conflict: none.

**ADR 380: The delegation in the agent block of an audit entry** (owner-tagged).

- Quote, decision 1: "The agent block of ADR 126 decision 10 holds seven fields in this fixed order".
- Effect: this change adds no agent block; audit-agent-block builds it.
- Conflict: none.

**ADR 240: A state download by a person is a reveal, and a principal has a kind.**

- Quote, Consequences: "the kind cannot change after the server makes the record". No route changes the kind on main (an update changes the display name only), so this change adds no rule and no error code for a change of the kind.
- Conflict: none.

## Open questions

none. Considered: a NEW error code `principal.kind-invalid` (refused: main answers `principal.invalid` for an unknown kind, and the code says enough); a NEW code `principal.kind-immutable` (refused: no route changes the kind, ADR 240 Consequences).

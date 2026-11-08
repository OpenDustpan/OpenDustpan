# 341. A taint that reaches a secret source is a routing change

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Changes ADR 202 decision 2: a taint that reaches a secret source in any export key is a routing change.

## Context

[ADR 202](0202-secrets-revisions-tags-drafts-and-rotation.md) decision 2 says that a routing change needs `profile:open` as well as `profile:write`. A routing change can change where a block sends secrets, an issued token or the result of a login block. The server decides it from the syntax of the two documents. The rule lists three kinds. The first is a difference in the text of a block of `providers` or `rotators`. The second is a difference in a path of the taint set of a block. The third is a difference in `imports` or `exports`.

[ADR 201](0201-the-profile-document-imports-and-expressions.md) decision 9 lists the export keys `variables`, `files`, `pulumi` and `opentofu`. Later ADRs add keys, for example `proxy` (ADR 330). A path can reach a secret source and an export key without a block input between them. A person with `profile:write` and no `profile:open` could then change what an export key holds.

## Decision

Decided by the project owner on 7 November 2026.

1. **The taint reaches an export key (owner, 2026-11-07).** Any change whose taint reaches a secret source, in any export key, is a routing change. It needs `profile:open` as well as `profile:write`. This holds for a save, a rollback, a tag move and the apply of a draft. It also holds for the referrers of the changed profile, as ADR 202 decision 2 says for blocks.
2. **An unknown export key fails closed (owner, 2026-11-07).** An export key can be unknown to the server. Then each change under that key is a routing change. A key becomes known only when an ADR lists it. The code that computes the taint must also handle it.

## Consequences

- A person who can write a profile and cannot open it can no longer change an export that reads a secret.
- A new export key is a routing-sensitive key from the first day. It needs no extra step to be safe.
- The error is the one that ADR 202 decision 2 gives (`identity.ErrDenied`, code `profile.routing-change-needs-open`). It names the export key and the path.
- The reference `docs/reference/profiles-api.md`, section "Routing changes", needs the new rule.
- Each defense step gets its own test. The tests cover a known key, an unknown key, and the check of the permission at the point of use.
- This ADR does not change the three kinds of routing change that ADR 202 decision 2 lists.

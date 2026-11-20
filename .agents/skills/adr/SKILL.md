---
name: adr
description: "Use for a question about a project decision: what we decided, or which rule applies now. Use also before you write or change an ADR or an RFC. Gives the lookup steps and the change links between ADRs."
---

# ADRs and RFCs of OpenDustpan

An ADR is a decision. An RFC is an open question. Write an ADR only after the
project owner accepts a decision. If a change contradicts an ADR, write an RFC
first.

## Find the rule that applies now

Do these steps before you use other tools:

1. Read `docs/adr/INDEX.md`. Find the ADR and the ADRs in its "Current rule"
   column.
2. Run `rg` over `docs/adr` and `docs/rfc` for the subject of the question.
3. Read each ADR in the "Current rule" column. A later ADR can change an
   earlier ADR.

Do not use Repowise as the source for a decision. Its index shows pushed
commits only, and it can be older than the checkout.

## Link an ADR that changes an earlier ADR

1. Write the item in the "Changes:" list of the new ADR.
2. Write the reverse item in the "Changed by:" list of the earlier ADR.
3. Run `make adr-index`.

ADR 296 gives the form of the items.

## Check

Run `make docs-lint`. It checks the ADRs from ADR 14 on and the index.

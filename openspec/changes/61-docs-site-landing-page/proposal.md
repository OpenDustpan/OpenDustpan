---
version: 1
---

## Why

**Persona: developer.** Developer tries to install the server and run a first stack or workspace from the docs page alone, but the docs site and landing page have no accepted scope until RFC 57 decides documentation as a product.

The user path this change must make work:

1. **Docs page.** Developer opens the docs page for install and first run of a stack or workspace
2. **Docs page.** Developer finds no site yet and uses the repository guides instead

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Docs page for install and first run does not exist as a site yet | Use the repository guides until the refined change ships the site. | `ls docs/guides` |

Evidence for the claims above:

- RFC 61 waits on RFC 57. Proof: | [RFC 61](rfc/0061-docs-site-and-landing-page.md) Docs site and landing page                        | documentation as a product (RFC 57)                             |

## What Changes

- Hold RFC 61 as waiting on documentation as a product (RFC 57) and refine scope, tooling, and hosting after RFC 57 is accepted.

Out of scope:

- RFC 57 documentation as a product
- Console views
- Server behavior changes

Depends on:

- 57-documentation-product
- `57-documentation-product` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- No user path changes until RFC 61 is refined; Developer still hunts install and first-run steps across README and guides.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main runs a durable scheduler that waits the first delay before its first claim. (`pkg/durable/scheduler_test.go:175 (func TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim)`)
- Main refuses to restore a dev backup into production. (`pkg/backup/restore_service_dev_test.go:100 (func TestRestoreRefusesDevBackupIntoProduction)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What is the scope of RFC 61: docs site, landing page, versioning, and hosting?
   - Options: Static site generated from the repository docs with versioned guides; Hand-maintained landing page plus separate docs host.
   - Provisional decision (owner review 14 Nov): Static site generated from the repository docs with versioned guides
   - Reason: Repository-sourced versioned docs keep guides testable and avoid drift between releases. Benchmark: Google: publish versioned product docs from the same repository with tested commands.
   - Persona impact: Developer gets tested first-run guides; no extra cost to other personas. Escape hatch: None; no implementation until RFC 57 is accepted and RFC 61 is refined.
2. Who owns the check that each command on the docs site runs as written?
   - Options: Docs toolchain tests every command on each docs page; Manual review of docs commands with no test.
   - Provisional decision (owner review 14 Nov): Docs toolchain tests every command on each docs page
   - Reason: Project rules require tested docs commands, so the refined change must name the harness and its owner. Benchmark: Google: every command on a docs page runs as written in automated tests.
   - Persona impact: Developer trusts first-run guides; writers pay the cost of keeping samples runnable. Escape hatch: None; docs commands stay untested until the refined change adds the test harness.

Questions considered and answered:

- Does any accepted ADR already decide the docs site or landing page? No. The ADRs in the pack decide namespaces, state reads, inventory, soft delete, federation, restore, and sign-in. None of them decides a docs site, a landing page, docs tooling, versioning, or hosting. This holding change touches no ADR.
- Can scope be fixed before RFC 57 is accepted? No. The roadmap row says RFC 61 waits on documentation as a product (RFC 57). Scope, tooling, versioning, and hosting stay open until RFC 57 is accepted.
- What answers the analytics and phone-home question? Project rules forbid phone-home code without the owner. The refined change must carry the no-tracking rule, already captured as the single holding requirement.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

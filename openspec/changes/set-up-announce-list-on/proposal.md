---
version: 1
---

Issue: #987.

## Problem

Problem: users have no way to get release and security news by email without tracking. The project has no announce list, so users must watch the issue tracker.

## Evidence

- The issue asks for an announce list on opendustpan.org with no tracking pixels and no click tracking. Proof: Issue 987 Scope: a list service on the domain with no tracking pixels and no click tracking.
- The owner decided an announce list on opendustpan.org. Proof: Issue 987 Decided: 69.1 (owner, 2026-10-18): an announce list on opendustpan.org.
- A discussion list is out of scope. Proof: Issue 987 Scope Out: a discussion list.

## Fix boundary

- Live announce-only list on opendustpan.org with tracking off
- Sign-up link in the docs guide
- Release checklist names the list for releases and security notices

Out of scope:

- Discussion list
- Server mail code or new API routes
- Chat channel

## Check

- Set up the hosted announce-only list on opendustpan.org with double opt-in and tracking off after the owner picks the service and record the address in NEW docs/guides/announce-list.md. A person's step. Check: No route: open the hosted sign-up page and confirm SPF, DKIM, DMARC pass with tracking off.
- Add the sign-up link to NEW docs/guides/announce-list.md. A person's step. Check: No route: the link in NEW docs/guides/announce-list.md opens the live sign-up page with no third-party scripts.
- Add the post-to-list step for each release and security notice to NEW docs/guides/release-checklist.md. A person's step. Check: No route: read NEW docs/guides/release-checklist.md and confirm it names the list, the poster, and the verify step.
- Confirm subscribe, plain message, and unsubscribe with a test address against NEW docs/guides/announce-list.md. A person's step. Check: No route: subscribe, confirm, read a plain notice, and unsubscribe with a test address.
- Run docs checks and the usability walk from the docs page alone for NEW docs/guides/announce-list.md. Confirmed by: CI (`make docs-lint`). Check: `make docs-lint` reports zero findings.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 74: No phone-home, ever** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 3 (current).
- Changed by: ADR 315 extends decision 6. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "The server never phones home. Phone-home is data about an installation, its use or its users that leaves the installation for a destination that the operator did not name."
- Quote: "No phone-home code. The open-source server holds no code that collects data for phone-home or sends it."
- Quote: "The server connects out only to destinations that the operator names. Examples are the database, the object store, sign-in providers, log sinks and metrics or trace collectors."
- Effect: The list is a hosted mail service with no server code. Messages carry no tracking. No installation data leaves the installation.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: no "main_facts": list what main already does (file:line and the function) from the pack.

Questions considered and answered:

- May announce mail or sign-up pages carry tracking? No. ADR 74 decision 1 forbids phone-home and the issue forbids tracking pixels and click tracking. The setup leaves tracking off and uses plain links.
- Does this change add server mail code or routes? No. ADR 74 decision 2 forbids phone-home code. The change adds no server code and no routes, only a hosted service plus docs links and a checklist step.
- Embedded form or plain link from project pages? Plain link. An embedded form would load third-party scripts on project pages, against ADR 74 decision 6 and the no-count rule cited in the issue.
- Which host replaces the open service choice? The owner-decided announce list on opendustpan.org. No other option remains because issue 987 records owner decision 69.1 for that list.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

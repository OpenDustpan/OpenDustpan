---
version: 1
---

Issue: #999.

## Problem

Regulated teams record each production change as a ticket in Jira Service Management or ServiceNow, and today they copy each change by hand. Nothing links the two records. This spike learns the shape of each interface before any product work.

## Evidence

- RFC 86 exists and describes the spike scope. Proof: docs/rfc/0086-change-tickets-in-external-change-systems.md exists in the pack paths
- No correlation_id use exists in product code. Proof: rg -n -F --glob '!*_test.go' 'correlation_id' pkg internal cmd matches 0
- The package pkg/changeticket does not exist on main. Proof: The pack paths list pkg/changeticket with exists false

## Fix boundary

- Scratch code and a spike doc live on a branch that is not for merge
- Six RFC 86 points are answered for each system

Out of scope:

- Adapters for merge
- Sync of comments, attachments or the CMDB
- Jira Data Center
- Any merge to main

## Check

- Write the NEW file `spike/scratch/main.go` with the scratch run of open, read, note and close. No route: the spike calls only the vendor APIs of the named sites. BLOCKED by named test sites and credentials. Check: The NEW path `spike/scratch/main.go` runs once against each named site and exits with code 0
- Write the NEW file `spike/changeticket-spike.md` with the six answers for each system. No route: the task writes a document only. BLOCKED by the run of the scratch code. Check: The NEW path `spike/changeticket-spike.md` answers the six points for each system
- Write the NEW file `spike/changeticket-cleanup.md` after closing each test ticket and deregistering each webhook. No route: the task calls only the vendor APIs of the named sites. Check: The NEW path `spike/changeticket-cleanup.md` lists each cleanup and each credential rotation
- Usability check: an owner reads the six answers from the NEW path `spike/changeticket-spike.md` alone. A person's step. Check: `git diff --stat main` lists only files under spike/

Depends on:

- Issue 999: owner answers 1 to 6
- Issue 999: named test sites and CI credentials

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has an unrelated session ticket store used only by session tests, not a change-ticket port (`pkg/session/sessiontest/memoryticketstore_test.go:14 (func TestMemoryTicketStore)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 225: Approval rules, approval defaults and ownership rules** (not owner-tagged).

- Decisions that apply: 7 (changed).
- Changed by: ADR 286 changes; ADR 299 changes decisions 1, 7 and 12. Chain to the end: ADR 286, ADR 299, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 286, 299.
- Quote: "Who may approve. An eligible approver of a requirement holds the approve action of the object (`changeorder:approve`, `job:approve` or `profile:approve`) and is in the `approvers` of its rule."
- Effect: The spike changes no approver rule. It records whether a ticket approval maps to a gate fact or needs a new signature kind.
- Conflict: none.

**ADR 226: Facts: typed, versioned fact documents** (owner-tagged: decision 10).

- Decisions that apply: 10 (current).
- Changed by: ADR 240 changes decision 4; ADR 241 extends decision 4; ADR 258 extends decision 7; ADR 263 changes decision 7; ADR 286 changes; ADR 342 extends decision 4; ADR 353 changes decisions 2 and 4; ADR 353 changes decision 10; ADR 353 extends decision 13; ADR 354 extends decisions 3 and 4. Chain to the end: ADR 240, ADR 241, ADR 258, ADR 263, ADR 286, ADR 342, ADR 353, ADR 354, ADR 297, ADR 248, ADR 313, ADR 343, ADR 358, ADR 366, ADR 365, ADR 301, ADR 333.
- Current rule: this ADR, with ADR 240, 241, 258, 263, 286, 342, 353, 354.
- Quote: "External checks (owner). An external check is a producer of facts."
- Effect: The spike changes no external-check timeout. It records whether a ticket answer can come from a server poll.
- Conflict: none.

## Open questions

1. Is a ticket approval a fact that a gate reads, or a new kind of signature? Decides: owner.
   - Options: A fact that a gate reads; A new kind of signature.
   - Recommended: A fact that a gate reads Reason: A fact keeps the approver rules and fails closed on a missing answer.
2. Can a ticket check wait up to 30 days, with its answer from a server read? Decides: owner.
   - Options: Wait up to 30 days with a server poll; Keep the current external-check timeout.
   - Recommended: Wait up to 30 days with a server poll Reason: A bounded wait with a poll gives a clear expiry and a record.
3. Which test sites and credentials unblock the live run? Decides: owner.
   - Options: Wait for one named Jira site and one named ServiceNow instance; Use public docs only.
   - Recommended: Wait for one named Jira site and one named ServiceNow instance Reason: A named throwaway site gives one clean run.

Not spec-ready: a code check fails: evidence path `pkg/changeticket` does not exist in the checkout; a code check fails: evidence path `pkg/changeticket` does not exist in the checkout; a code check fails: task "Write the NEW file `spike/scratch/main.go` with the scratch " names no file: name the file on main (path:line) or the NEW path that it writes; a code check fails: and 2 more.

Questions considered and answered:

- Does this change add a route, store or adapter on main? No. The issue says nothing merges to main. Scratch code lives only under the NEW path `spike/scratch/`.
- Do RFC 51 or RFC 86 decide the design? No. An RFC is an open question. ADR 225 decision 7 and ADR 226 decision 10 stay as written.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

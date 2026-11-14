---
version: 1
---

Issue: #2194.

## Problem

As a Developer, I run the Pulumi CLI against the server and read history after each change. A journaled up of a program with a StackReference prints success but stores a snapshot mismatch error event, so I cannot trust the stored history. The same up with journaling disabled stores no such event.

## Evidence

- The failing shape is a YAML program with a StackReference and secret outputs. Proof: Issue #2194 gives the Pulumi.yaml with type pulumi:pulumi:StackReference, additionalSecretOutputs outputs, and outputs apiKeyOut, literalSecret, plainOut.
- The server has a journal decoder and replayer at these paths. Proof: Pack paths: internal/pulumijournal, pkg/stack/journal_build.go, pkg/stack/journal_entries.go all exist.
- The cause is not proven. Proof: Issue #2194 Result says the text appears to come from the CLI check that compares the snapshot built from the journal with the snapshot of the engine, and the issue says not investigated.

## Fix boundary

- A first reproduction names the cause from a diff of the journal-built snapshot with the engine snapshot of a journaled and a non-journaled up of the issue program.
- A replay fix keeps the dropped StackReference field so the stored events hold no snapshot mismatch error event.
- An e2e step and a docs note record the StackReference behavior, including the sealed secret outputs.

Out of scope:

- Change of the Pulumi CLI
- Change of journal version negotiation
- Migration of checkpoints already stored by the older replay

## Check

- Run the YAML program of the issue with the pinned Pulumi CLI of e2e/tool.go, with and without journaling, and record the first differing field of the checkpoint and the journal entries. Check: The recorded diff names one field, or records no server difference.
- Add a failing replay test in NEW pkg/stack/journal_stackreference_test.go that feeds the journal entries of that run and compares the built checkpoint with the engine snapshot. Check: The NEW test fails on main with the mismatch, or records that replay matches the engine snapshot.
- Fix the decoder or replayer in internal/pulumijournal and pkg/stack/journal_build.go for the named field. This task runs only when the diff shows a server difference. Check: NEW pkg/stack/journal_stackreference_test.go passes.
- Add a hostile test that stored events and logs hold no plain-text secret value after the journaled up. Check: The hostile test in NEW pkg/stack/journal_stackreference_test.go passes.
- Add an e2e step with the real Pulumi CLI and the issue program in NEW e2e/journal_stackreference_test.go. The CLI uses an access token and the route ANY /api/stacks/{}/{}/{}/update/{}/journalentries. The step reads the stored events and checks for no mismatch event. Check: make e2e passes the new step. Confirmed by: CI (NEW e2e/journal_stackreference_test.go).
- Add the NEW page docs/guides/pulumi-journaling.md with the StackReference behavior and the command to compare with journaling off. Check: A person runs the page commands as written.
- Run the Developer path from the page alone and run make check. A person's step. Check: `make check` reports zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The server answers a journal version above 0 at the start of an update. (`internal/api/pulumi/updates.go:484 (func Handler.journalVersionFor)`)
- The e2e suite caps the journal version for comparison runs. (`e2e/journalcap.go:29 (func NewJournalVersionCap)`)
- The store starts a Pulumi update through StartUpdate. (`pkg/store/postgres/updates.go:152 (func Store.StartUpdate)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR decide how StackReference state is journaled? The pack lists no ADR and no RFC for this item, so no quoted decision applies. The work stays in the decoder and replayer named by the issue.
- Does the fix belong in the server journal replay or in the CLI check? The issue says the text appears to come from the CLI check and says not investigated. The first tasks compare journaled and non-journaled runs and name the first differing field before any replay fix.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

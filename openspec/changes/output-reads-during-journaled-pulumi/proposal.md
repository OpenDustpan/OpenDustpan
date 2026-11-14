---
version: 2
---

Issue: #1642.

## Why

**Persona: developer.** Developer reads the outputs of a producer stack from a consumer job while the producer runs a journaled update, and needs one predictable answer for what latest state means instead of a read that sometimes replays an unfinished update.

The user path this change must make work:

1. **API.** Consumer unit reads the outputs of a producer stack through GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}/outputs while a journaled update of the producer runs.
2. **Docs page.** Reader follows docs/guides/output-sharing.md for the latest-state rule during a running update.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Consumer unit has no sharing rule from the producer | 403 with error code output-sharing.denied names the missing sharing rule and the command that lists the sharing rules of the namespace. | `GET /api/dustpan/v1/namespaces/{namespace}/output-sharing` |
| Caller holds no unit:read-outputs grant on the consumer | 403 with error code unit.forbidden names the missing unit:read-outputs grant and the command that lists the units of the namespace. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| Producer stack has no completed checkpoint yet | 404 with error code unit.unknown-state names the missing completed checkpoint and the command that lists the units of the namespace. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |

Evidence for the claims above:

- The output sharing guide exists at the path the change updates. Proof: docs/guides/output-sharing.md
- The pack names no ADR for this item, so no ADR quote applies. Proof: Context pack adrs list is empty and the issue text cites no ADR.
- Journaled updates append through stored batches. Proof: TestAppendJournalNeedsAWriterForAJournaledUpdate in pkg/stack/journal_append_test.go:62
- The tool-neutral outputs route exists on main. Proof: Pack main list: GET /api/dustpan/v1/namespaces/{}/units/{}/outputs

## What Changes

- Output reads during a journaled Pulumi update return the outputs of the last completed checkpoint and name its state version id.
- docs/guides/output-sharing.md states that the running journal is not part of latest state for consumer reads.

Out of scope:

- Rebuilding a deployment from journal batches on output reads
- Changes to the export of a stack by its owner
- New output sharing rules or settings

Depends on:

- `output-sharing` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A consumer may read outputs that are older than the running update; the answer names the completed state version id so the staleness is visible.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Journaled updates append through stored batches. (`pkg/stack/journal_append_test.go:62`)
- Built-in roles hold the output sharing actions. (`pkg/access/access_test.go:1723`)
- Checkpoint conflict has a catalogued error code. (`internal/errcode/catalog_update.go:12`)
- Audit log defines the read-access kind. (`pkg/audit/audit.go:58`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Must an output read replay the journal batches of a running update? No. Reads serve one stored completed checkpoint by stable state version id and never replay journal batches, so cost stays bounded at one checkpoint load.
- Can the owner export and a consumer read see different latest states during an update? Yes. The owner export keeps its rebuild of a running update from stored batches. Consumer output reads serve the last completed checkpoint only, and the guide states the difference.
- Which check wins when the grant, the sharing rule, and the checkpoint are all missing? Authorization first. A read without unit:read-outputs or without a sharing rule returns 403 denied even when the checkpoint is missing.
- Which ADR decision lines apply to this change? The pack proves the negative: the adrs list is empty and the issue text cites no ADR, so no quoted decision line exists to list.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

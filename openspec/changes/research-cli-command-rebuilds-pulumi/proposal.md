---
version: 1
---

Issue: #983.

## Problem

An operator needs an exit path so a backup can become state that the Pulumi CLI or OpenTofu can use with another backend.

## Evidence

- The migrate-out guide exists today. Proof: docs/guides/migrate-out.md exists true
- The backup domain package exists on main. Proof: pkg/backup listed in main facts

## Fix boundary

- A research note with a proposal and its limits for rebuilding Pulumi or OpenTofu state from a backup.

Out of scope:

- No CLI command
- No server route
- No backup format change

## Check

- Write the NEW `docs/research/rebuild-state-from-backup.md` note with the mapping, the secret path, the placement, and the limits. No route: the task reads backup docs and writes a document only. Check: ls `NEW docs/research/rebuild-state-from-backup.md`
- Confirm the note passes docs checks and states the provisional placement and the RFC need. Check: make docs-lint

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main keeps a portable backup manifest test. (`pkg/backup/portable_test.go:267 (TestPortableManifestFormat)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 39: Commands never touch the database** (not owner-tagged).

- Decisions that apply: 1 (changed), 4 (changed).
- Changed by: ADR 237 changes decisions 1 and 4; ADR 238 changes decisions 1 and 4; ADR 245 changes decisions 1 and 4. Chain to the end: ADR 237, ADR 238, ADR 245, ADR 252, ADR 285, ADR 324, ADR 315, ADR 356, ADR 295, ADR 253, ADR 297, ADR 301, ADR 312, ADR 320, ADR 333, ADR 332.
- Current rule: this ADR, with ADR 237, 238, 245.
- Quote: "Only the server opens the data. `opendustpan serve` is the only command that opens the database, the blob store, a backup target or the key-encryption key. Every other command is a client of the REST API. It needs only network access to the server."
- Effect: The note keeps the server-only rule. An offline rebuild that reads backup files needs a new narrow RFC exception. A server route needs no exception.
- Conflict: rfc_needed.

**ADR 42: A portable backup format** (not owner-tagged).

- Decisions that apply: 2 (current), 7 (current).
- Changed by: ADR 248 changes; ADR 253 changes decision 4. Chain to the end: ADR 248, ADR 253, ADR 286.
- Current rule: this ADR, with ADR 248, 253.
- Quote: "Only in the server. The export runs in the server, behind the backup routes of ADR 30. The import runs in the server, behind the restore routes and maintenance mode of ADR 38 (ADR 39). No command reads or writes a portable part."
- Effect: The note uses the portable SQL parts and blob keys as the only input and changes no format.
- Conflict: none.

## Open questions

1. Does the rebuild run offline on backup files only, or through the server? Decides: PM.
   - Options: Offline command on backup files only, with an RFC exception to ADR 39; Server route that reads a backup; Hybrid: server re-encrypts secrets, offline command rebuilds plain state.
   - Recommended: Hybrid, stated in the note as provisional. Reason: ADR 39 and ADR 42 keep the data in the server, so placement needs a decision before any build.

Not spec-ready: the change conflicts with ADR 39: an RFC comes first.

Questions considered and answered:

- Does the rebuild need the server, or only the backup files? ADR 39 decisions 1 and 4 keep data access in the server. An offline rebuild needs an RFC exception. The note records offline, server route, and hybrid paths with no build.
- Which parts of the portable backup map to Pulumi and OpenTofu state? ADR 42 decisions 2 and 7 name the input: one JSONL gzip file per table plus blobs. The mapping of those rows to a Pulumi DIY backend layout and to one OpenTofu state file per workspace is the research.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

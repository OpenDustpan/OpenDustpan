---
rfc: 62
title: Pulumi journaling
status: draft
created: 2026-10-11
---

# RFC 62. Pulumi journaling

Status: draft.

## Problem

The Pulumi CLI can send small per-operation journal entries instead of whole
checkpoints. ADR 37 (decision 9) keeps journaling off: `StartUpdate` answers
`journalVersion` 0, so the CLI saves full or delta checkpoints. This RFC
collects the spike evidence for issue #275 and makes the call. It refers to
ADR 37 and RFC 39 and changes neither.

A throwaway prototype answers the journal protocol behind an opt-in setting
on the branch `spike/pulumi-journaling`
(<https://github.com/OpenDustpan/OpenDustpan/tree/spike/pulumi-journaling>).
The prototype never merges. It stays off by default, and with the setting
off the server behaves exactly as on main (`journalVersion` 0, no journal
route). The prototype answers `journalVersion` 1
(`internal/api/pulumi/journal.go`), not 2 as the issue asked. Question 5
says why.

## Method

All CLI evidence comes from one `TestPulumiJournaling` run on base
`6a1f7fe8cde6772950c96cfcbcf5986bb6f25791` (main with #318 and #271),
2026-10-11 21:59 to 22:59 UTC, exit 1: real Pulumi CLI v3.250.0, SQLite, fs
blobs, shared 4-core machine. The only failed step is `up_change#03`, the
3,000-resource full-checkpoint change, which hit the 15-minute step limit.
The raw lines are in `spike/journal-measurements.log` on the spike branch
(entries from 22:0x onwards). Before #318 the full and delta modes were
slower. See `spike/journal-measurements-before-318.log` on the same branch.

The harness e2e gate is off for the spike (E2E=no). The evidence is the
command below. The journaling e2e cases live in `e2e/journal_test.go` on the
spike branch as `TestPulumiJournaling`, which runs only when
`DUSTPAN_EXPERIMENTAL_PULUMI_JOURNALING=1` is set, so the default suite stays
as on main. Repeat a run with:

```sh
DUSTPAN_EXPERIMENTAL_PULUMI_JOURNALING=1 DUSTPAN_JOURNAL_SIZES=1000,3000 go test -tags e2e -count=1 -v -run TestPulumiJournaling -timeout 100m ./e2e/
```

Wall time is the time of `pulumi up` for create and for change. Wire bytes
are request bodies of state saves counted at the server. Server times are
microsecond timers in the spike build, summed over create and change.

Pulumi source references below name files in pulumi/pulumi at commit
2255563. No network history beyond those files was reachable. Where a number
needs it, the cell says "not measured" and says why.

## Question 1. Benefit

Time, save counts and wire bytes for stacks of 1,000 and 3,000 resources,
with full checkpoints, with delta uploads (cutoff 1), and with journaling
(version 1), from the run above. Each cell is create / change.

| Resources | Full checkpoints | Delta uploads (cutoff 1) | Journaling (version 1) |
| --- | --- | --- | --- |
| 1,000 time | 104 s / 188 s | 58 s / 97 s | 12 s / 14 s |
| 1,000 saves | 2,005 / 2,003 | 1 verbatim + 2,004 deltas / 1 + 2,002 | 132 / 131 batches |
| 1,000 wire bytes | 24.3 MB / 46.1 MB | 1.74 MB / 1.29 MB | 152 KB / 155 KB |
| 3,000 time | 844 s / over 900 s (step limit, did not finish) | 440 s / 792 s | 44 s / 61 s |
| 3,000 saves | 6,005 / 3,519 before the limit | 1 + 6,004 / 1 + 6,002 | 382 / 381 batches |
| 3,000 wire bytes | 202 MB / 233 MB before the limit | 5.2 MB / 3.8 MB | 450 KB / 460 KB |

Export size was 3,017,131 bytes at 1,000 resources and 9,047,131 bytes at
3,000 resources, in every mode.

Server time breakdown, summed over create and change:

| Mode | Breakdown |
| --- | --- |
| 1,000 full | JSON decode 46.5 s, resource count 38.4 s, gzip 9.6 s, body read 8.7 s, SHA-256 7.3 s, others under 6 s each |
| 1,000 delta | resource count 38.3 s, delta apply 31.5 s, gzip 9.2 s, blob write 5.4 s, old-blob delete 2.2 s, SQL commit 1.9 s |
| 3,000 delta | resource count 339 s, delta apply 258 s, gzip 74 s, blob write 19 s, old-blob delete 7.8 s, SQL commit 6.6 s |
| 3,000 journaling | resource count 19.6 s, gzip 4.6 s, SHA-256 4.0 s, others under 1.2 s each |

The cost is the number of saves times the checkpoint size. Delta uploads cut
wire bytes about 20 times but not the number of saves, and each save still
does work on the full checkpoint (count, apply, gzip). Journaling cuts both:
about 15 times fewer saves (the CLI batches entries) and small bodies.
Server CPU and memory as a whole were not measured. The timers above are the
evidence for server work.

10,000 resources is an extrapolation. Full and delta grow about
quadratically (3 times the resources gave about 4 to 8 times the time), so
10,000 is far above any client timeout. Journaling grew about 3.7 to 4.3
times from 1,000 to 3,000, so about 3 to 4 minutes per update at 10,000.

## Question 2. Correctness matrix

Each row compares `pulumi stack export` with journaling on against the same
update with journaling off, after normalization. The normalization drops the volatile manifest, the
metadata, the created and modified times, the provider-assigned ids and the
provider references. It sorts resources by URN, because the checkpoint lists
them in engine completion order and the replay in journal sequence order.
The run column names the run in Method for every row.

| Case | Result | Run | Note |
| --- | --- | --- | --- |
| create | pass | 2026-10-11 21:59 to 22:59 UTC | Equal after normalization. |
| update in place (property change, same resources) | not tested | 2026-10-11 21:59 to 22:59 UTC | The sized runs replace one reference (delete plus create), which is not a plain in-place change. |
| replace (swap one resource) | pass | 2026-10-11 21:59 to 22:59 UTC | Same basis as create. |
| add a resource | pass | 2026-10-11 21:59 to 22:59 UTC | Same basis as create. |
| no-op update | pass | 2026-10-11 21:59 to 22:59 UTC | Same basis as create. |
| delete a resource | pass | 2026-10-11 21:59 to 22:59 UTC | Same basis as create. |
| refresh | pass | 2026-10-11 21:59 to 22:59 UTC | Same basis as create. |
| refresh that deletes | pass, tolerant | 2026-10-11 21:59 to 22:59 UTC | The run logged `refresh after stack rm exit 1` in both modes. The row counts as pass only because the exports after it compared equal in both modes. The step records either CLI outcome and the run does not assert which occurred. |
| failed operation | pass | 2026-10-11 21:59 to 22:59 UTC | A reference to a missing stack fails the update server-side. The export after recovery compares. |
| cancel of a held update | pass | 2026-10-11 21:59 to 22:59 UTC | The checkpoint survives the cancel. The export after cancel compares. |
| crash in the middle | pass with journaling on | 2026-10-11 21:59 to 22:59 UTC | With journaling on, the export after the crash equals the pre-crash export (true). Full mode logged false. The full-checkpoint path is not part of the spike's question, so this stays an observation. |
| `pending_operations` | not tested | 2026-10-11 21:59 to 22:59 UTC | The failed update's export held none in either mode (`failed export holds pending operations: false`), with the key fixed to `pending_operations`, so the case did not happen. |
| state migration entries | not tested end to end | 2026-10-11 21:59 to 22:59 UTC | The replayer refuses migration entries (unit test `TestReplayRejectsStateMigration`). The prototype answers version 1, so the CLI never sent one in the run. Version 2 would change this. See question 5. |
| destroy | pass | 2026-10-11 21:59 to 22:59 UTC | Same basis as create. |

The run's failed step, `up_change#03`, is the 3,000-resource
journaling-off change (step timeout). Every other step passed.

## Question 3. How to get the replayer

Three options: import the Pulumi Go module, port the replayer with
attribution (Apache-2.0 is compatible with the license rules), or write a
new one. The prototype ports `pkg/backend/journal.go` (`NewJournalReplayer`,
`Add`, `GenerateDeployment`, about 920 lines) into `internal/pulumijournal`
with an Apache-2.0 header naming pulumi/pulumi and commit 2255563.

Cost of the import, measured once from a throwaway scratch module outside
the repository importing `github.com/pulumi/pulumi/pkg/v3/backend` at
v3.250.0 (since deleted):

| Measure | Value | How |
| --- | --- | --- |
| `go mod graph \| wc -l` | 2008 | `go mod tidy` then `go mod graph` in the scratch module |
| Modules required (`go list -m all \| wc -l`) | 473 | Same scratch module |
| Module download size | about 24 MB | `cache/download/github.com/pulumi/` after `go mod tidy` |
| Unpacked module size | about 107 MB | Module cache `github.com/pulumi/pulumi/` |
| `govulncheck ./...` | 16 vulnerabilities from 8 modules | `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` in the scratch module |

Keeping an import current means tracking every CLI journal change (see
question 5) through up to 473 modules, each with its own vulnerabilities.
Keeping a port current means re-applying upstream replayer diffs by hand at
each CLI upgrade. A new replayer from scratch costs the most and risks
behavior drift against the only other implementation. The ADR 7 age rule
also applies to any new dependency.

## Question 4. Storage

The prototype makes a checkpoint blob from the journal after each accepted
batch: it replays the base checkpoint with every entry received so far and
saves the rebuilt deployment through the normal `SaveCheckpoint` path, which
replaces the update's checkpoint. Sessions live only in server memory,
keyed by update. Complete and cancel drop them at once. When the registry
is full, the next insert first drops sessions untouched past the 30-minute
TTL, then refuses the new session while still full. No background sweep
runs, and a crash loses all sessions. Recovery after a crash uses the last
saved checkpoint, and the next update starts a fresh session on that base.
Nothing about ADR 37 changes: each save writes one write-once blob key, SQL
keeps the metadata, backups copy what SQL names. Journal entries themselves
are never stored. Only the rebuilt checkpoint is. Crash recovery of
unflushed entries is therefore "at most the last batch is lost", same as an
interrupted checkpoint save.

## Question 5. Protocol risk

Journal versions 1 and 2 are not published specs. CLI history at commit
2255563 (local copies of the journal files, with no network access for more):

- `sdk/go/common/apitype/journal.go`: about 30 commits between 2025-09 and
  2026-10, including the journaling API itself (#20343, 2025-10-22), a
  version on each entry (#20599), sequence-id validation (#20611), parent
  undangling (#20683), URN normalization (#21578), lossless non-UTF8 strings
  (#23872), state migration persistence (#24327), and migration ordering
  fixes (#24552).
- The client-side journaler (`httpstate/journal*.go`): about 17 commits,
  including batching and channel rework in 2025-12 (#21105, #21183, #21185,
  #21211).
- `journalVersion`: 5 commits, ending with v2 state migration activation
  (#24458, 2026-09-22).

The prototype answers version 1, not 2 as the issue asked, because version
2 makes the CLI send state migration entries that the port refuses: CLI
#24458 ("Activate journal v2 state migrations") turns migrations on at
version 2, and the ported replayer answers 400 and saves nothing on such an
entry (`ErrStateMigration`). Production support at version 2 needs
migration-entry replay first. The spike measured version 1 only. No size-10
run with version 2 passed.

The format moves several times a year, sometimes with behavior the server
must match (ordering, normalization, migration transactions). What the CLI
does when the server rejects an entry is not measured: the prototype
answers 400 and saves nothing (unit-tested), but no e2e run shows whether
the CLI retries the batch, falls back to a checkpoint, or fails the update.

## Question 6. State reading

Replay forces the server to parse checkpoint contents: the deployment JSON,
every resource (URN, type, inputs, outputs, dependencies, parent,
provider), pending operations, the secrets manager state, snippets,
extension parameterization, and rebuilt base state. State migration entries
are refused. The parse stays limited to replay in the prototype: nothing is
indexed, searched, or shown, and no other feature reads the parsed form. But
the glossary rule ("OpenDustpan stores state but never reads its contents")
still breaks at the parse step, so adopting journaling needs RFC 39
(reading state contents) to move first, as the issue states. Journaling
pulls in only the replay half of RFC 39, not a general index or browser.

## Options

1. **Propose journaling next, together with issue #343.** Journaling is the
   only measured path that brings large stacks into a reasonable time
   (3,000 resources: 44 s create, 61 s change, against 440 s and 792 s with
   delta uploads). Issue #343 ("Pulumi checkpoint saves: per-save server
   work that does not scale with checkpoint size") is a complement, not a
   replacement: even if it cuts the resource count (339 s at 3,000
   delta) and halves gzip, the 3,000 delta update stays above 10 minutes
   (delta apply alone is 258 s).
2. **Adopt later.** Keep ADR 37 decision 9 (`journalVersion` 0) until a user
   with a large stack asks for journaling, or until a 3,000-resource delta
   update fits into a reasonable time. Costs nothing now. The spike evidence
   and prototype stay on the throwaway branch for the revisit. The measured
   facts against this option: delta uploads do not fit today (440 s create,
   792 s change at 3,000 resources), and #343 alone does not close the gap
   either.
3. **Reject.** Stay on full and delta checkpoints only. The facts against
   this option: at 3,000 resources a full change already exceeds the
   15-minute step limit, and delta still needs about 13 minutes for one
   change, while journaling needs about 1 minute.

A separate axis is the replayer source (question 3): import, port, or new.
The evidence favors the port over the import (473 modules and 16
vulnerabilities for one package), but the port still needs an owner at each
CLI upgrade (about 30 journal format changes in a year, see question 5).

## Proposal

Propose journaling next. The project owner turns this into an ADR. This RFC
does not write one. Take issue #343 with it: #343 cuts per-save server work
but does not replace journaling for large stacks (see Options).

The honest cost:

- The port of the replayer (about 920 lines, Apache-2.0 attribution) and
  its upkeep at each CLI upgrade (the journal format changed about 30 times
  in a year, see question 5).
- The RFC 39 decision first: replay reads state (see question 6).
- The production work list below.
- Journal version 2: version 2 needs migration-entry replay first (see
  question 5). The spike measured version 1 only.

## What production support would need

The prototype is throwaway and intentionally lacks all of the following.
This list is the handoff if the call is accepted. Review round 1 asked for
each. None was added beyond what makes the spike observable, and none
should be mistaken for production support.

- A bounded session registry (max sessions, per-session entry and resource
  caps) so abandoned updates cannot grow memory without limit.
- A background idle sweeper as the backstop for clients that vanish without
  ending their update (the prototype only drops idle sessions when the
  registry is full).
- Retry idempotency keyed on sequence or operation identity, once the CLI's
  retry behavior on reject is measured.
- Domain sentinel mapping for every replay-build failure so the CLI gets
  the status it expects (400 for bad entries, nothing saved).
- Route tests that build the handler with journaling on and run the full
  authorization and action-declaration assertions over the gated route.
- Migration-entry replay for journal version 2 (see question 5).

## Open questions

- What does the CLI do when the server answers 400 to a journal batch
  (retry, checkpoint fallback, or fail)?
- Why does the export after a crash differ from the pre-crash export with
  journaling off, and does that change the journaling call? (Kept as an
  observation. The full-checkpoint path is not part of the spike's
  question.)
- Which replayer source does the project accept, and who owns it at each
  CLI upgrade?
- How does RFC 39 move so that replay has a decided basis?
- What does migration-entry replay for journal version 2 look like?

## Outcome

None yet.

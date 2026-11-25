---
version: 2
---

Issue: #2041.

Records the release gate of work item 19 (#423) in the first-tag checklist.
Journaling turns on in `main` now; the first tag that ships it waits for
#456, #672, #419 and #3207.

SECURITY: yes. The change makes the journal batch route, a parser of
untrusted input, reachable in the release build. The pull request keeps a
Claude review (rev2) next to the other reviews.

## Why

**Persona: developer.** A developer runs `pulumi up` against the server with
a Pulumi CLI from v3.250.0 to v3.268.0. Today every update saves a full
checkpoint after each step, one save after the other, because the release
build answers `journalVersion` 0 to every start. The S3 latency benchmark of
#2041 measured about 36 s more for an update with 200 steps at a database
round trip of 10 ms, and about 150 s more at 50 ms. RFC 62 measured
journaling at 3,000 resources at 44 s for a create and 61 s for a change,
against 440 s and 792 s with delta uploads. ADR 43 decision 1.1 says
"Journaling is on by default". The owner confirmed the intent on
2026-11-15: "The intention is for journaling to be on by default."

**Persona: operator.** An operator installs the server from the docs and
expects the documented default. The operator turns journaling off with one
setting when a journal batch is refused, and follows the steps of the
operator reference.

The user path this change must make work:

1. **CLI.** The developer runs `pulumi up` with a CLI in the verified range.
   The start answers the highest journal version that the CLI offered and
   that the server supports (2 for a CLI from v3.264.0, else 1). The CLI
   sends journal batches. `complete` builds the checkpoint. `pulumi stack
   export` gives the same normalized export as a run with journaling off.
2. **CLI, outside the range.** A CLI older than v3.250.0, newer than
   v3.268.0, a development build or an unknown `User-Agent` gets
   `journalVersion` 0 and saves checkpoints, as today.
3. **Setting.** The operator sets `DUSTPAN_DISABLE_PULUMI_JOURNALING=true`
   (or `--disable-pulumi-journaling`, or the file key
   `disable-pulumi-journaling`) and restarts the server. Each new start
   answers 0. An update that already runs keeps its mode.
4. **Docs page.** `docs/reference/journaling.md` states that journaling is
   on by default in the release build, names the setting, and keeps the
   steps for a refused batch and the bounds table.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The server refuses a journal batch with an entry it does not know | 400 that names the entry by its sequence ID. The update fails. | `DUSTPAN_DISABLE_PULUMI_JOURNALING=true`, restart, then `pulumi up` (steps of `docs/reference/journaling.md`) |
| `complete` with `succeeded` after a refused or missing batch | 409 with error code `journal.incomplete`: a journal batch was not stored. The stack stays locked. | `pulumi cancel` |
| An update past a journal bound | 413 with one sentence that names the bound, its count and its limit | `pulumi up` with fewer resources |
| The build at `complete` passes its time bound | 503. The update stays active. | `pulumi cancel` |

Evidence for the claims above:

- The release build answers 0. Proof: `internal/api/pulumi/journal_release.go:9`
  (`const maxSupportedJournalVersion = 0`, in the file that only the release build compiles).
- The release build has no journal writer. Proof:
  `pkg/dustpan/journal_release.go:10-13` returns `nil, nil`.
- The setting exists with the default `false`. Proof:
  (read only) `internal/cli/settings.go`, line 393-397.
- The 409 message and error code exist. Proof:
  `pkg/stack/journal_build.go:346` and
  `internal/api/pulumi/journal_refusals_recording_test.go:73`.
- The 413 replay bound message exists. Proof:
  `internal/api/pulumi/journal_replay_bounds_test.go:102-120`.

## What Changes

- The build tag `journalrecording` goes away. Every build answers the
  negotiated journal version of ADR 43 decision 2.1, with a supported
  version of 2, and wires the journal writer with the replay verifier.
- `--disable-pulumi-journaling` keeps its default `false` and its effect:
  `true` answers 0 to each new start (decision 1.2).
- A preview or a dry run answers `journalVersion` 0 whatever the CLI offers
  (decision 1.5). Main does not do this yet: the start reads only the
  `User-Agent` and the offer.
- The e2e step `TestPulumiJournalingBothModes` proves journaling on and off
  for each CLI in the verified range: v3.250.0, v3.262.0 and v3.268.0. For a
  CLI in the range, the on run must answer a non-zero version and the server
  must accept batches. For v3.200.0 (under the gate) the on run must answer 0.
- The journal e2e steps (`TestPulumiJournalingOn`,
  `TestPulumiJournalingOnVersion`, `TestPulumiJournalRefusalCancel`,
  `TestPulumiJournalStateMigration`) run on the release build.
- One list in `e2e/tool.go` names the verified journal CLIs. A test fails
  when the newest or the oldest verified CLI version of the gate is not in
  that list (work item 19, second acceptance criterion of #423).
- `docs/reference/journaling.md`, `docs/guides/load.md` and the route
  coverage allowlist stop saying that the release build answers 0.
- `make journal-guard`, `make lint-journal-build` and `make journal-test`
  go away. `make e2e-journal` stays and runs the verified CLI list.
- The release gate moves from the build to the first-tag checklist. The
  gate of the first tagged release in `docs/roadmap.md` gets one open item:
  no tag ships journaling before #456, #672, #419 and #3207 are done.

Out of scope:

- A change to the gate range, the bounds or the replay.
- Sealing journal batch blobs with the item key of the update (ADR 151
  decision 6). Checkpoint blobs are not sealed on main either. That work
  belongs to the item key steps (#1378).
- The output reads of #1642 (change `output-reads-during-journaled-pulumi`).
- The upstream watch (work item 16, #420). Work item 19 does not name it.

Depends on: none for the switch on `main`. The first tag that ships
journaling depends on these issues (the release checklist item of this
change records them):

- #456: the build holds no full copy of the base for kinds 7 and 10, and
  the memory benchmark covers a state migration (decisions 7.3 and 9.4).
- #672: the build time bound is measured against the time after which the
  CLI stops waiting at `complete` (decision 9.5).
- #419: the nightly run with journaling on and off (work item 15,
  decision 11.1).
- #3207: the recorded corpus of decision 11.3 for each verified CLI
  release (work item 7, which work item 12 needs).

## Usability

Where the path still costs the person time or surprises them:

- A CLI newer than v3.268.0 saves checkpoints until a server release
  verifies it (decision 5.2). The developer sees no error, only the slower
  update. The operator reference says so.
- A server error during a journaled update fails the `pulumi up`, because
  the CLI has no fallback. The operator reference gives the steps.
- A last batch that never arrives is not visible to the server (decision
  10.6). The operator reference says so and names `pulumi refresh`.

## What main already does

Each fact names its file, line and function on main at ddf630a81. The change specifies only the difference.

- The gate answers the lower of the offer and the supported version for a CLI from v3.250.0 to v3.268.0. (`internal/api/pulumi/journal.go:96 (func negotiateJournalVersion)`)
- The oldest verified CLI release of the gate is v3.250.0. (`internal/api/pulumi/journal.go:14 (func oldestVerifiedJournalCLI)`)
- The newest verified CLI release of the gate is v3.268.0. (`internal/api/pulumi/journal.go:22 (func newestVerifiedJournalCLI)`)
- With the setting `true`, a start answers 0 before the gate runs. (`internal/api/pulumi/updates.go:486 (func journalVersionFor)`)
- The start stores the negotiated version on the update row for every kind, a preview too. (`pkg/stack/updates.go:546 (func StartBy)`)
- The build skips a non-mutating update. (`pkg/stack/journal_build.go:358 (func buildBeforeEnd)`)
- The handler copies the constant into `supportedJournal`. (`internal/api/pulumi/pulumi.go:253 (func NewHandler)`)
- The release build answers 0. (`internal/api/pulumi/journal_release.go:9 (const maxSupportedJournalVersion)`)
- The recording build answers 2. (`internal/api/pulumi/journal_recording.go:8 (const maxSupportedJournalVersion)`)
- The recording build wires the journal writer with the verifier. (`pkg/dustpan/journal_recording.go:15 (func journalWriter)`)
- The release build has no journal writer and returns `nil, nil`. (`pkg/dustpan/journal_release.go:10 (func journalWriter)`)
- The server calls `journalWriter(cfg)` in both builds. (`pkg/dustpan/dustpan.go:2129 (func newServices)`)
- `TestPulumiJournalingBothModes` runs in `make e2e` on the release build, where both runs answer 0, so it compares two checkpoint runs. (`e2e/journaling_test.go:20 (func TestPulumiJournalingBothModes)`)
- The release build answers no journal version above 0 in the e2e suite. (`e2e/journaling_build_release_test.go:8 (func journalingBuildAnswersNonzero)`)
- The e2e step reads that answer to choose what the on run must show. (`e2e/journaling_test.go:130 (func checkJournalingOn)`)
- `make e2e-journal` runs four journal steps on the recording build with v3.268.0 only, and not `TestPulumiJournalingBothModes`. (`Makefile:385 (make target e2e-journal)`)
- The default Pulumi matrix is v3.262.0, v3.250.0 and v3.200.0. (`e2e/tool.go:108 (func Pulumi)`)
- The route coverage test excuses the journal route in the release suite. (`e2e/coverage_pulumi.go:267 (func journalAllowEntries)`)
- `make check` runs `lint-journal-build`, `journal-guard` and `journal-test`. (`Makefile:15 (make target check)`)
- The target `lint-journal-build` lints the recording build. (`Makefile:47 (make target lint-journal-build)`)
- The target `journal-test` runs the journal route tests of the recording build. (`Makefile:434 (make target journal-test)`)
- The target `journal-guard` refuses a release path that uses the recording tag. (`Makefile:437 (make target journal-guard)`)
- The journal batch blob key has the namespace prefix and one key for each attempt. (`pkg/stack/journal_entries.go:757 (func journalKey)`)

## Preconditions of ADR 43 (analysis at ddf630a81)

Decision 2.4 and work item 19 name the preconditions. Work item 19:
"A release answers a non-zero journal version only after the work finishes
items 11 to 15 and 18 (decision 2.4)." Each item that they need is listed
too. Each file in the Evidence column is only read.

| Item | State | Evidence |
| --- | --- | --- |
| 1 Setting and answer 0 (#404) | partial, done here | setting (read only) `internal/cli/settings.go`, line 393-397, answer 0 when off `internal/api/pulumi/updates.go:486`. Decision 1.5 (previews answer 0) is not in code: `journalVersionFor` and `Updates.StartBy` (`pkg/stack/updates.go:505-546`) do not read the kind. This change adds it. |
| 2 Version gate (#405) | done | `internal/api/pulumi/journal.go:14-24`, `internal/api/pulumi/journal.go:96` |
| 3 Package and depguard (#406) | done | (read only) `.golangci.yml`, line 116; port `pkg/stack/journal_entries.go:173` |
| 4 Generated types (#407) | done | (read only) `internal/pulumijournal/internal/gen/gen.go`, (read only) `internal/pulumijournal/internal/gen/pin.go`; `make generate` (`Makefile:428`, target `generate`) |
| 5 Decoder (#408) | done | (read only) `internal/pulumijournal/decode.go`, line 62 |
| 6 Version 1 replayer (#409) | done | (read only) `internal/pulumijournal/replay.go`, line 20-28 |
| 7 Corpus (#410, closed) | not done | 5 cases, kinds 0, 1 and 10 only, no recorded CLI release ((read only) `internal/pulumijournal/testdata/corpus`). Open: #3207 |
| 8 Entry storage (#411) | done | `pkg/stack/journal_entries.go:534`, key `pkg/stack/journal_entries.go:757` |
| 9 Journal route (#412) | done | `internal/api/pulumi/routes.go:118` |
| 10 Streaming build (#413) | partial | two-pass build (read only) `internal/pulumijournal/replay.go`, line 23-25; kinds 7 and 10 hold the base in memory ((read only) `internal/pulumijournal/replay.go`, line 404, (read only) `internal/pulumijournal/migration.go`, line 77). Open: #456 |
| 11 Refusals, complete, cancel (#414) | done | `pkg/stack/journal_build.go:346`; e2e (read only) `e2e/journaling_refusal_test.go`, line 24 (decision 10.4), green in `e2e-journal` |
| 12 Version 2 (#415) | done, needs 7 | (read only) `internal/pulumijournal/migration.go`, line 189; e2e (read only) `e2e/journaling_migration_test.go`, line 27 (decision 2.3) |
| 13 Bounds (#417, closed) | partial | numbers (read only) `internal/pulumijournal/bounds.go`, line 32-48, `docs/reference/journal-limits.md`; decision 9.5 not measured ("The project has not yet checked whether the CLI waits past 20 minutes"). Open: #672, #456 |
| 14 Both modes (#418, closed) | not done in effect | the step runs only on the release build, where both runs answer 0 (`e2e/journaling_test.go:20`). This change completes it. |
| 15 Nightly run (#419) | not done | the nightly workflow runs `make e2e` on the release build and opens no issue (`.github/workflows/e2e-nightly.yml:103`). Open: #419 |
| 16 Upstream watch (#420) | not done, not a precondition | work item 19 does not name it |
| 17 Upgrade report (#421) | done | (read only) `internal/api/server/upgrade.go`, line 33 |
| 18 Operator reference and glossary (#422) | done, text changes here | `docs/reference/journaling.md`, (read only) `GLOSSARY.md`, line 782 |
| 19 Release gate (#423) | this change | the first-tag checklist in `docs/roadmap.md` holds the tag until the open items above are done |

Why the release build answers 0: the build tag split came with the journal
route in #453 (commit 202271801, "The recording build answers journal
version 1 ... the release build answers 0 and wires none. A make target
guards the release paths against the journalrecording tag."). The reason is
decision 2.4: "No release answers a non-zero journal version before the
work meets decisions 2.2, 2.3, 9 and 10.4."

## ADR check

Each quote is copied from the file in `docs/adr/`. A decision is
owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when
the ADR records decisions of the project owner.

**ADR 43, Pulumi journaling support.** Not owner-tagged: "the project owner
gave the decisions on RFC 62 and RFC 64 to the project lead, and the project
lead accepted this ADR." The owner confirmed decision 1.1 on 2026-11-15.
Changed by: ADR 151 (decisions 6 and 10), ADR 220 (decision 6). Current rule
(`docs/adr/INDEX.md`): "this ADR, with ADR 151, 220". ADR 281 also changes
decision 6.1 (one key for each attempt) without a change link (#2187).

- 1.1: "**Journaling is on by default.**" and "It is a `boolSetting` with the default `false`." The setting is `--disable-pulumi-journaling` (`DUSTPAN_DISABLE_PULUMI_JOURNALING`).
- 1.2: "**Off answers version 0.** When the setting is `true`, start-update
  and `begin-update` answer `journalVersion` 0."
- 1.4: "**Every binary can replay.** Each binary holds the replay code, with
  the setting on or off."
- 1.5: "**Previews answer 0.** The server answers `journalVersion` 0 to a
  preview and to a dry run. The CLI never journals them (R3)."
- 2.1: "The server answers the highest journal version that the CLI offered
  and that the server supports, only to a CLI that the version gate lets
  through (decision 5)."
- 2.4: "**The release gate.** No release answers a non-zero journal version
  before the work meets decisions 2.2, 2.3, 9 and 10.4."
- 5.2: "The cost: a CLI newer than that constant saves checkpoints until the
  operator upgrades the server to a binary that verified it."
- 11.4: "The e2e suite runs each supported CLI version with journaling on
  and with journaling off. Both runs must give the same normalized `stack
  export`, as ADR 37 asks for delta uploads."
- Work item 19: "A release answers a non-zero journal version only after
  the work finishes items 11 to 15 and 18 (decision 2.4)."

This change follows decisions 1.1, 1.2, 1.5, 2.1 and 11.4. It meets
decision 2.4 and work item 19 although it lands before their open items:
both restrict a release ("No release answers a non-zero journal version";
"A release answers a non-zero journal version only after"), and no tag
exists (`git tag -l 'v*'` prints nothing; ADR 279: no release before
milestone 7). A build of `main` is not a release. The change moves the gate
from the build tag to the first-tag checklist in `docs/roadmap.md`, which
names #456, #672, #419 and #3207 as open items of the first tag. So no tag
can answer a non-zero journal version before the work meets decisions 2.2,
2.3, 9 and 10.4. The PM decided this order on 2026-11-15 under the owner's
delegation. No conflict.

**ADR 151, Redaction, and an item key for each history item.** Owner-tagged:
"The owner approved every decision of this ADR on 2026-10-17". Changed by:
ADR 154, ADR 210, ADR 217, ADR 220. Decision 6: "A journal batch (ADR 43)
seals the batch blob with the item key of the update." Main does not seal
Pulumi checkpoint or journal batch blobs yet (`pkg/stack/journal_entries.go:534`
puts the plain body; `pkg/stack/updates.go:1353` puts the plain checkpoint).
This change does not touch sealing and does not change the order of writes.
No conflict.

**ADR 220, Isolation between namespaces on shared infrastructure (owner).**
Owner-tagged. Changed by: ADR 273, ADR 286, ADR 363. Decision 7: "Each blob
key of a namespace starts with `namespaces/<namespace ID>/`." Main does this
for journal keys (`pkg/stack/blobscope.go:20`). This change does not change
keys. No conflict.

**ADR 281, Any S3-compatible store: the database decides which write wins.**
Owner-tagged: "Decided by the project owner on 2026-10-27." Changed by:
ADR 340. Work item 2: "Pulumi journal batches: the same." Main does this
(`pkg/stack/journal_entries.go:757`). No conflict.

**ADR 37, Pulumi checkpoints in blobs, and delta checkpoint uploads.**
Decision 9 ("No journaling") is superseded by ADR 43 decision 12.1. No
conflict.

## Open questions

none

Questions considered and answered:

- Does the switch need an owner decision? No. ADR 43 decision 1.1 decides
  the default, and the owner confirmed it on 2026-11-15. Decision 2.4 and
  work item 19 restrict a release, and the first-tag checklist holds them.
- Can the switch land on `main` before the preconditions? Yes. Decision 2.4
  names a release, and no tag exists. With journaling on in `main`, every
  `make e2e` run and the nightly run exercise the journal path before the
  first tag, which the open items need anyway.
- Must ADR 151 sealing of journal batches land first? No. ADR 43 and
  ADR 151 do not put it in the release gate. Checkpoint blobs carry the
  same content and are not sealed on main either. Journal blobs live only
  until the build (ADR 43 decision 6.7).
- Is #2194 ("snapshot mismatch" error event in a journaled up with a stack
  reference) a precondition? Not by the words of ADR 43. The on and off
  export comparison of this change fails if the server builds other state.
  #2194 stays open for its own investigation.
- Which CLIs prove decision 11.4? The verified range is v3.250.0 to
  v3.268.0. The pinned archives with checksums exist for v3.250.0,
  v3.262.0 and v3.268.0 (`e2e/tool.go:81-92`). v3.264.0 is the first CLI
  that offers 2, so v3.268.0 covers version 2 and v3.250.0 and v3.262.0
  cover version 1.
- What happens to the CI jobs that call removed targets? A cloud session
  cannot push `.github/workflows`. The pull request body carries the
  workflow patch. The workflow change merges first: it stops calling
  `journal-guard`, `lint-journal-build` and `journal-test`. Then this
  change removes the targets.
- Does a test still prove the release build cannot answer above 0 by
  accident? No such test is wanted after this change. The tests that stay
  prove the negotiated answer for each `User-Agent` form and the answer 0
  with the setting `true`.

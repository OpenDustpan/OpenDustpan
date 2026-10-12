# Research: Pulumi journaling, change history and error behavior

Question (issue #275 follow-up): Why does the Pulumi CLI journal format look
unstable, what changed between Pulumi CLI versions, and what does the CLI do
when the server refuses a journal batch?

Date: 2026-10-12

This note gives facts. It does not make a recommendation. RFC 62 (Pulumi
journaling, a draft that is not on `main` at this date) holds the
recommendation.

## 1. Summary and scope

### Summary

The Pulumi CLI can send per-operation journal entries to
`PATCH /api/stacks/{org}/{project}/{stack}/update/{updateID}/journalentries`
instead of whole or delta checkpoints. The engine part landed in v3.194.0
(#19862) but stayed local. The first release that sends entries to a backend
is v3.204.0 (#20343), and only with `PULUMI_ENABLE_JOURNALING=true` and a
server that answers `journalVersion` 1. From v3.225.0 (#22041) journaling is
on by default. From v3.264.0 (#24458) the CLI offers version 2 and accepts 1
or 2. Section 3 lists 51 commits from 2025-09-08 to 2026-10-03: 37
client-only, 7 additive, 3 semantic and 4 wire-break. Three of the
wire-breaks added data under the unchanged version 1 (entry kinds 8 and 9,
the `requiresByteString` flag). The fourth changed the version 2 shape
before a release negotiated version 2. Server-relevant changes did not slow
down: 2026-06 to 2026-09 has 9 of the 14 rows that are not client-only.

In measured runs with real CLIs (section 6), no failed update lost the base
state of an existing stack, on the journal path or on the checkpoint path.
The journal path kept the base plus the entries that the server accepted.
The checkpoint path kept the base plus 1 pending operation. A second
`pulumi up` with no fault succeeded. Both paths make 4 attempts for a 500
and do not retry a 400, and the CLI never falls back from the journal to
checkpoints. In 3 runs the CLI ignored a 400 on the last journal batch: it
exited 0, sent `complete` with `succeeded`, and the stored state did not
hold the refused entry. When the server refused `complete`, the CLI exited
with an error and the update stayed `in-progress`, so the next `pulumi up`
failed with 409.

### Scope

The project owner asked why the journal format looks unstable and what
changed between Pulumi CLI versions. RFC 62 left three gaps:

- It named the commits that touch journaling, but did not classify them.
- It measured one CLI version (v3.250.0).
- It had no network history of pulumi/pulumi. Its references name files at
  one commit (2255563) only.

This note closes these gaps with a full clone of pulumi/pulumi (sections 3
to 5) and a measurement with three CLI versions (section 6). Sections 7 and
8 list the requirements and the open questions that follow from the facts.

## 2. Sources and access

- Clone: `git clone --filter=blob:none https://github.com/pulumi/pulumi`.
  The clone worked. The proxy did not block it.
- `master` at `2255563bcc` (2026-10-09). Tags up to `v3.268.0`
  (`git tag | sort -V | tail -1`).
- Commit bodies are the squash-merged pull request descriptions, so
  `git show` was enough. No web page was used.
- The Pulumi Cloud service code (`pulumi/pulumi-service`, referred to in
  #20991, #21576, #24417 and #24458) is private. It was not read.
- Citation format: `path@sha` (file at that commit or tag), `#NNNNN` (pull
  request number from a commit subject), `CHANGELOG.md@2255563bcc:line`, and
  "measured, see section 6". A path without `@sha` is at `2255563bcc`. Line
  numbers are at `2255563bcc` unless a `@sha` is given.
- Tag commits: v3.200.0 `0625026c40`, v3.204.0 `24654399ab`, v3.225.0
  `ca6228e034`, v3.250.0 `7549635909`, v3.264.0 `32519795ba`, v3.268.0
  `128d3f2ec9`. Dates in this note are author dates.
- The journal files at v3.268.0 are equal to those at `2255563bcc`.
  `git diff --stat v3.268.0 2255563bcc` shows no change in
  `sdk/go/common/apitype/journal.go`, `pkg/backend/journal.go`,
  `pkg/backend/journal_state_migration.go`,
  `pkg/backend/httpstate/journal/snapshot.go`,
  `pkg/engine/journal_snapshot.go`, `pkg/backend/httpstate/client/api.go`,
  `sdk/go/common/util/httputil/http.go` and
  `pkg/backend/httpstate/token_source.go`. Changes on `master` after
  v3.268.0 touch `CompleteUpdateRequest` and stack outputs, not journaling.

## 3. Change history

### Method

- Files: `sdk/go/common/apitype/journal.go`, `pkg/backend/journal.go` (the
  replayer; it never moved, `git log --follow` starts at #19862),
  `pkg/backend/journal_state_migration.go` (replayer part, from #24327) and
  `pkg/backend/httpstate/journal/snapshot.go` (the cloud journaler, from
  #20343).
- The journal version has no constant before #24327. #20343 writes the
  literal `1` in `pkg/backend/httpstate/client/client.go` and `backend.go`.
  `apitype.LatestJournalVersion` (`sdk/go/common/apitype/updates.go:118`)
  appears in #24327 with value 2 and has not changed since.
- Selection: every commit whose diff in `backend.go`, `client/client.go`,
  `client/api_endpoints.go`, `apitype/updates.go`, `env/env.go` or
  `httpstate/snapshot.go` matches `[Jj]ournal`
  (`git log --full-history --no-merges -G`), plus #20740 and #22671. These
  two change journal wiring in `backend.go` without the word in the changed
  lines, and both have CHANGELOG entries about the journaler.
- First journaling commit: `44cde74c9a` (#19862, 2025-09-08). Earlier commits
  that mention "journal" (for example #20199 and #20288) rename the old test
  journal. They are not journaling.
- First release of a commit:
  `git tag --contains <sha> | grep -E '^v3\.[0-9]+\.[0-9]+$' | sort -V | head -1`.

Kinds:

- **client-only**: the server gets the same bytes and must do the same
  thing. All commits before v3.204.0 are client-only, because no CLI sent
  entries to a server before v3.204.0 (section 4). They define the version 1
  baseline that the first sending CLI shipped.
- **additive**: a new optional field, route, entry kind or behavior that a
  server built to the earlier rules can ignore and still build the same
  state, or that the CLI uses only after a new negotiation.
- **semantic**: no new wire data, but the replay rules change. A server
  replayer must match the new rules to build the same state.
- **wire-break**: new data sent under the same negotiated version that an
  older reader either refuses or ignores, with a different state as result.

The kind of each pre-API row rests on
`pkg/backend/httpstate/backend.go@0625026c40` lines 1665 to 1680 (v3.200.0
builds the journal on a `ValidatingPersister`, not on the network) and on
`pkg/backend/httpstate/client/client.go@24654399ab` (the first
`journalentries` call). The source of each row is `git show <sha>` of that
row and the files named above.

### Table A

| Commit (PR) | Date | First release | Kind | What a replaying server needs to do |
| --- | --- | --- | --- | --- |
| 44cde74c9a (#19862) | 2025-09-08 | v3.194.0 | client-only | Nothing. Engine journal and local replayer (kinds 0 to 6) only feed a local shadow checkpoint. Pre-API baseline. |
| c67e7f93f3 (#20526) | 2025-09-25 | v3.198.0 | client-only | Nothing then. Adds `DeleteOld/DeleteNew` and `PendingReplacementOld/New` to the entry. Pre-API baseline. |
| 0d82e76095 (#20559) | 2025-09-29 | v3.199.0 | client-only | Nothing then. Creates `apitype.JournalEntry` and makes the replayer work on apitypes "to re-use the journal replayer in the service" (commit body). Pre-API baseline. |
| 3f8bc36253 (#20599) | 2025-09-29 | v3.199.0 | client-only | Nothing then. Adds `version` to each entry. Pre-API baseline. |
| adf7dacfef (#20610) | 2025-09-30 | v3.199.0 | client-only | Nothing. Simpler journal interface. Fixes a missing sequence ID on write entries before an entry left the CLI. |
| 9330eb1563 (#20616) | 2025-09-30 | v3.199.0 | client-only | Nothing. Test diagnostics. |
| 2c7b8cf87b (#20611) | 2025-10-01 | v3.199.0 | client-only | Nothing. Local check that `sequenceID != 0`. The body says the code is shared with the service. |
| 2b1ed7f281 (#20614) | 2025-10-01 | v3.199.0 | client-only | Nothing. Adds an unused `POST .../update/{id}/journal` client call (`{"entry": ...}`), only reached from benchmarks. Removed in #20343. |
| 8890f5ddf0 (#20596) | 2025-10-03 | v3.200.0 | client-only | Nothing then. Adds kind 7 `rebuilt-base-state` and its replay rule. Pre-API baseline. |
| 67605a6c70 (#20740) | 2025-10-15 | v3.203.0 | client-only | Nothing. Shadow-journal errors no longer fail the update. |
| 433cca32cf (#20683) | 2025-10-07 | v3.202.0 | client-only | Nothing then. Parent undangling after refresh in `rebuildDependencies`. Pre-API baseline. |
| cfdb0ec213 (#20676) | 2025-10-07 | v3.202.0 | client-only | Nothing. Secrets provider refactor in the shadow-journal wiring. |
| 3ffd519d3a (#20707) | 2025-10-10 | v3.202.0 | client-only | Nothing. Turns the local shadow journal off. |
| 3882b627ba (#20343) | 2025-10-22 | v3.204.0 | additive | New optional `journalVersion` in the start-update request and response, and the new route `PATCH .../journalentries` with `{"entries": [...]}`. A server that ignores the field answers 0 and the CLI keeps checkpoints. Opt-in by `PULUMI_ENABLE_JOURNALING`. |
| b44349c26b (#20716) | 2025-11-04 | v3.206.0 | client-only | Nothing. Fewer local decrypts. The replayer API returns `TypedDeployment`. |
| 72334612d3 (#20875) | 2025-11-05 | v3.206.0 | client-only | Nothing. Turns the local shadow journal on again. |
| 32235a17d2 (#20693) | 2025-11-11 | v3.207.0 | semantic | `rebuildDependencies` (after a refresh) now also processes `ReplaceWith`. The loop shadows `i` and appends `""`, so it does not prune correctly. Fixed by #23927. A server that matches this CLI must know that the rule changed again in v3.254.0. |
| f6959e1e3d (#20923) | 2025-11-12 | v3.208.0 | client-only | Nothing. Unused parameter. |
| 4ced576925 (#20991) | 2025-11-19 | v3.208.0 | client-only | Nothing for the journal. Batches secret encryption per entry (fewer `batch-encrypt` calls for the service secrets provider). |
| c4d771df23 (#20994) | 2025-11-21 | v3.209.0 | client-only | Nothing. Helper refactor. |
| 90d07433aa (#21105) | 2025-12-02 | v3.210.0 | additive | Same request shape. Requests now carry up to 100 entries (`PULUMI_JOURNALING_BATCH_SIZE`), with at most one request in flight. A 413 answer makes the CLI split the batch. Before this, each entry was its own concurrent request. |
| d6277b37b3 (#21112) | 2025-12-02 | v3.210.0 | client-only | Nothing. `Close` may run more than once. |
| e3e887c51d (#21021) | 2025-12-02 | v3.210.0 | client-only | Nothing. Stops the local comparison when journaling is on. |
| f770d66d60 (#21117) | 2025-12-03 | v3.210.0 | client-only | Nothing. Lock fix. |
| 18bdd783a8 (#21183) | 2025-12-10 | v3.211.0 | client-only | Nothing in the format. Elided entries (unchanged sames) no longer wait for the server. They still go in the next batch. |
| 5791d209ea (#21185) | 2025-12-11 | v3.211.0 | client-only | Nothing in the format. Batching fixes (full-batch detection, asynchronous flush). |
| 3ee0d93462 (#21211) | 2025-12-12 | v3.212.0 | client-only | Nothing. Channels instead of locks. |
| a2491acb0f (#21578) | 2026-02-03 | v3.219.0 | semantic | The replayer now runs `NormalizeURNReferences()` inside `GenerateDeployment`. The body says that the cloud side forgot this step. A server must normalize URN references after replay. |
| 8fd6fd0754 (#21576) | 2026-02-06 | v3.220.0 | client-only | Nothing in the format. An entry that depends on an elided entry waits for the batch of that entry and fails if it failed, so the CLI stops sending dangling references. |
| f5dc5a3fff (#21709) | 2026-02-09 | v3.220.0 | client-only | Nothing. Local integrity metadata gets env vars. |
| 7bdffa4ed6 (#22041) | 2026-03-02 | v3.225.0 | additive | The request carries `journalVersion: 1` by default. `PULUMI_ENABLE_JOURNALING` is removed and `PULUMI_DISABLE_JOURNALING` turns journaling off. A server that answers 0 is not affected. |
| bd2962ec26 (#22267) | 2026-03-18 | v3.227.0 | client-only | Nothing. License header. |
| 72e4960156 (#22299) | 2026-03-20 | v3.228.0 | client-only | Nothing. Adds the route to the endpoint name table of the CLI. |
| e9796252cf (#22671) | 2026-05-20 | v3.243.0 | client-only | Nothing. Journal setup errors now fail the update instead of a nil panic. |
| 2464bdd71a (#23564) | 2026-06-24 | v3.249.0 | wire-break | New kind 8 `extension-parameterize` with `extensionRef` and `extension`, sent with `version: 1` under negotiated version 1. Replay must add the pair to `Extensions`. An older reader ignores it (no `default` case then) and loses the map, or refuses it. Sent only when a program uses extension parameterization. |
| e85ffb01cb (#23650) | 2026-06-29 | v3.249.0 | wire-break | New kind 9 `snippets` with `snippets`, `version: 1`, negotiated version 1. Replay must replace `base.Snippets` and set the feature `snippets-prototype`. An older reader drops the change. Sent only when the snippets option is used. |
| 8e0e60d5ae (#23747) | 2026-07-01 | v3.250.0 | client-only | Nothing. Package move. |
| c2238c0ce9 (#23914) | 2026-07-15 | v3.254.0 | client-only | Nothing. Package move. |
| d8d70bf3a3 (#23927) | 2026-07-15 | v3.254.0 | semantic | `rebuildDependencies` now prunes dangling `ReplaceWith` URNs and keeps valid ones. A server must match. |
| bc9da5c151 (#23940) | 2026-07-15 | v3.254.0 | client-only | Nothing. Standard library swap. |
| 2c439508ef (#23943) | 2026-07-16 | v3.254.0 | client-only | Nothing. Package move. |
| e00f356fea (#23872) | 2026-07-21 | v3.254.0 | wire-break | New entry flag `requiresByteString` under version 1. Replay must set the deployment feature `byteString` (schema version 4) if an applied entry has it. An older reader that ignores it writes a deployment without the feature, which the commit says older engines would then corrupt. Set only when state holds strings that are not UTF-8. |
| baf78e1b31 (#21241) | 2026-08-07 | v3.257.0 | additive | New `POST .../begin-update` with `journalVersion` (capability `begin-update`). Reverted by #24291 in the same release, so no release ships it. |
| 3b576c7d99 (#24291) | 2026-08-12 | v3.257.0 | client-only | Nothing. Revert of #21241. |
| ec6f65b9fe (#24450) | 2026-08-25 | v3.260.0 | client-only | Nothing. `errors.AsType`. |
| 6879979328 (#24327) | 2026-08-31 | v3.261.0 | additive | Defines version 2 and kind 10 `state-migration` (entry `version: 2`, at that time with `removeOlds`, `states`, `baseStatePatches`, `newStatePatches`). `LatestJournalVersion = 2`, but the client still offers and accepts only 1, so no CLI sends kind 10. The replayer now refuses unknown kinds and versions. |
| a2fcd246a8 (#24552) | 2026-09-09 | v3.262.0 | wire-break | The version 2 shape changes: `layout` (`[{baseIndex}\|{stateIndex}]`) replaces `removeOlds`. Not shipped in a negotiated form: no release before v3.264.0 negotiates version 2. |
| a2c9dd11d1 (#24417) | 2026-09-17 | v3.264.0 | additive | `POST /api/stacks/{org}/{project}/{stack}/begin-update` again, behind the capability `begin-update` v1. Request and response carry a top-level `journalVersion`. The response `deployment` is optional. |
| 769aa46fd7 (#24458) | 2026-09-22 | v3.264.0 | additive | The client offers `LatestJournalVersion` (2) and journals at 1 or 2. At 2 it may send kind 10. A server that answers 1 or 0 is not affected. At 1 the CLI refuses state migrations. Body: "Needs pulumi-service#49631". |
| 6e03a45e7e (#24721) | 2026-09-29 | v3.266.0 | client-only | Nothing. Update-started event plumbing near the journal setup. |
| bdfa394093 (#25015) | 2026-10-03 | v3.268.0 | client-only | Nothing. Lint (`new(expr)`). |

### Counts per kind

| Kind | Rows |
| --- | --- |
| client-only | 37 |
| additive | 7 |
| semantic | 3 |
| wire-break | 4 |
| Total | 51 |

### Pace (rows per month)

| Month | All rows | Rows that are not client-only |
| --- | --- | --- |
| 2025-09 | 6 | 0 |
| 2025-10 | 8 | 1 |
| 2025-11 | 6 | 1 |
| 2025-12 | 7 | 1 |
| 2026-01 | 0 | 0 |
| 2026-02 | 3 | 1 |
| 2026-03 | 3 | 1 |
| 2026-04 | 0 | 0 |
| 2026-05 | 1 | 0 |
| 2026-06 | 2 | 2 |
| 2026-07 | 6 | 2 |
| 2026-08 | 4 | 2 |
| 2026-09 | 4 | 3 |
| 2026-10 (to 10-09) | 1 | 0 |

The total pace dropped after 2025-12: 27 rows in the first four months, 24
in the next ten. The server-relevant pace did not drop: 5 rows that are not
client-only from 2025-09 to 2026-05, and 9 from 2026-06 to 2026-09. All 4
wire-breaks are from 2026-06 or later.

## 4. CLI version support

The e2e suite runs the Pulumi CLI at "3.200.0", "3.250.0" and "latest"
(v3.268.0 on 2026-10-12).

Conditions for all journaling versions:

- The CLI journals only for an operation that is not a preview and not a
  dry run (`kind != apitype.PreviewUpdate && !dryRun`,
  `pkg/backend/httpstate/backend.go:2210`, the same guard at each tag). That
  covers `up`, `refresh`, `destroy` and `import`.
- The URL segment after the stack is always the literal `update`, for all
  operation kinds (`getUpdatePath`,
  `pkg/backend/httpstate/client/client.go:540-543`, not changed since
  `feaea31f7b`, 2018).
- Journaling is not an entry in `/api/capabilities`. The start-update
  exchange alone negotiates it.

### v3.200.0 (`0625026c40`, 2025-10-03), e2e "3.200.0"

- Highest version offered: none. `StartUpdateRequest` has no
  `journalVersion` (`git grep JournalVersion v3.200.0` finds nothing in
  `sdk/go/common/apitype/updates.go`).
- What it sends: no journal route. The CLI runs a shadow journal into a
  local `backend.ValidatingPersister` next to the real snapshot manager and
  saves checkpoints as before (`pkg/backend/httpstate/backend.go@0625026c40`
  lines 1655 to 1680). If the shadow snapshot is different, it emits an
  engine `errorEvent` "snapshot mismatch: ..." (same file, lines 1690 to
  1705), which goes to the server through the engine-events route
  (`pkg/backend/display/events.go@0625026c40:256-261`). The client has an
  `AppendUpdateJournalEntry` (`POST .../journal`), but no code in httpstate
  calls the code path that uses it (`git grep NewJournaler v3.200.0` shows no
  httpstate caller).
- Observed on the wire: with a server that answers `journalVersion` 1,
  v3.200.0 sends no journal request and sends 15 `PATCH .../checkpoint`
  requests per update for the 5-resource program (measured, see section 6).
- `PULUMI_DISABLE_JOURNALING`: no effect. The variable does not exist at this
  tag (`sdk/go/common/env/env.go@0625026c40` has neither `EnableJournaling`
  nor `DisableJournaling`).

### v3.204.0 (`24654399ab`, 2025-10-22), first journaling release

- Highest version offered: 1, as a literal, only if
  `PULUMI_ENABLE_JOURNALING` is `1` or `true`
  (`pkg/backend/httpstate/client/client.go@24654399ab:848-849`; truth values
  in `sdk/go/common/util/env/env.go:338-347`; the `PULUMI_` prefix in the
  same file, line 127).
- Conditions: it journals when the response `journalVersion == 1` and
  `PULUMI_ENABLE_JOURNALING` is set
  (`pkg/backend/httpstate/backend.go@24654399ab:1651`). The commit body says
  it needs "server support first, and also setting
  `PULUMI_ENABLE_JOURNALING=true`" (#20343). All other answers give
  checkpoints.
- What it sends: `PATCH .../update/{id}/journalentries` with
  `{"entries": [<one entry>]}`, one request per entry. The engine goroutine
  that made the entry sends the request, so requests can run at the same
  time (`pkg/backend/httpstate/journal/snapshot.go@24654399ab`,
  `AddJournalEntry` calls `SaveJournalEntry` directly; #21105 body: "rather
  than sending all entries concurrently"). Kinds 0 to 7, `version: 1`
  (`sdk/go/common/apitype/journal.go@24654399ab:25-32`). Gzip body, retry
  policy "all methods" (`client.go@24654399ab:1255`). It also runs the
  legacy snapshot manager with a no-op persister for comparison
  (`backend.go@24654399ab:1651-1663`).
- `PULUMI_DISABLE_JOURNALING`: no effect. It does not exist yet. Journaling
  is opt-in.

From v3.225.0 (`ca6228e034`) the default changes: the request carries
`journalVersion: 1` unless `PULUMI_DISABLE_JOURNALING` is true, and the
variable `PULUMI_ENABLE_JOURNALING` no longer exists (#22041;
`CHANGELOG.md@2255563bcc:2142`).

### v3.250.0 (`7549635909`, 2026-07-01), e2e "3.250.0"

- Highest version offered: 1, as a literal, unless
  `PULUMI_DISABLE_JOURNALING` is true
  (`pkg/backend/httpstate/client/client.go@7549635909:1577-1578`).
  `LatestJournalVersion` does not exist yet.
- Routes: `POST /api/stacks/{org}/{project}/{stack}/update/{id}` (start
  update) carries the offer. There is no `begin-update` (it shipped in
  v3.264.0). Entries go to `PATCH .../update/{id}/journalentries`
  (`client.go@7549635909:2356`).
- Conditions: it journals only when the response `journalVersion == 1` and
  the variable is not set (`backend.go@7549635909:2068`). An answer of 2 or
  more keeps checkpoints.
- Entry kinds: 0 to 9 (`sdk/go/common/apitype/journal.go@7549635909:25-34`),
  all `version: 1`. Kind 8 only for extension parameterization, kind 9 only
  with the snippets option. No `requiresByteString` (added in v3.254.0).
- Batching: up to 100 entries per request, a flush every 50 ms, one request
  in flight, a split on 413 (`pkg/backend/httpstate/journal/snapshot.go`
  from #21105, #21185 and #21211, all in v3.212.0 or older).
- 429 is not retried at this tag
  (`sdk/go/common/util/httputil/http.go@7549635909` has no
  `TooManyRequests` branch; #24131 added it in v3.256.0).
- `PULUMI_DISABLE_JOURNALING` true: the request omits `journalVersion`
  (`omitempty`), and a non-zero answer does not turn journaling on
  (`backend.go@7549635909:2068`). The CLI then saves checkpoints.
- Observed on the wire: with a server that answers 1, the CLI sends 8
  journal requests for the 5-resource program, about 60 ms apart, with 16
  entries (`sequenceID` 1 to 16) in batches of 1, 1, 1, 1, 1, 5, 5 and 1.
  With `PULUMI_DISABLE_JOURNALING=true` it sends no journal request and 15
  `PATCH .../checkpoint` requests (measured, see section 6).

### v3.268.0 (`128d3f2ec9`, 2026-10-07), e2e "latest"

`git tag | sort -V | tail -1` gives `v3.268.0`.

- Highest version offered: `apitype.LatestJournalVersion` = 2, unless
  `PULUMI_DISABLE_JOURNALING` is true (`client.go:1618-1619` for start
  update, `client.go:1681-1682` for begin-update; the constant at
  `sdk/go/common/apitype/updates.go:118`).
- Routes: if `/api/capabilities` lists `begin-update` version 1 and
  `PULUMI_DISABLE_BEGIN_UPDATE` is not `"true"`, the offer goes in
  `POST /api/stacks/{org}/{project}/{stack}/begin-update`
  (`backend.go:1870-1890`, `apitype/service.go:235-237`). If not, it goes in
  create update plus `POST .../update/{id}` (`backend.go:1904-1915`).
  Entries go to `PATCH .../update/{id}/journalentries`
  (`client.go:2453-2461`).
- Conditions: it journals when the answer is 1 or 2 and the variable is not
  set (`cloudPersistenceUsesJournal`, `backend.go:1982-1988`). All other
  answers keep checkpoints. The code comment calls this the way to change
  the API without breaking older clients.
- Entry kinds: 0 to 10 (`sdk/go/common/apitype/journal.go:24-36`). The CLI
  sends kind 10 only when version 2 was negotiated
  (`pkg/engine/journal_state_migration.go:30,47`). The entry `version` is 2
  for kind 10 and 1 for all other kinds (`pkg/backend/journal.go:136-139`).
- State migrations: with version 1 negotiated, the resource monitor does not
  advertise the state-migration feature and refuses a registration that
  carries migrations with "state migrations are not supported by this
  deployment" (`backend.go:1992-1997`,
  `pkg/resource/deploy/source_eval.go:983-985,2396-2398`). With checkpoints
  or with version 2, migrations are allowed.
- `PULUMI_DISABLE_JOURNALING` true: the request omits `journalVersion`, and
  the CLI ignores a non-zero answer (`backend.go:2209-2211`). It saves
  checkpoints only. The local shadow journal still runs at
  `LatestJournalVersion` (`backend.go:2228-2231`).
- Observed on the wire: with a server that answers 1, v3.268.0 journals at
  version 1 with the same request and batch pattern as v3.250.0. With
  `PULUMI_DISABLE_JOURNALING=true` it sends no journal request and 15
  `PATCH .../checkpoint` requests (measured, see section 6). Version 2 was
  not measured.

### Version 2 releases

- v3.261.0 to v3.263.0 define `LatestJournalVersion = 2` but still offer
  and accept only 1 (`client.go@ceb2e86de7:1614-1615`,
  `backend.go@ceb2e86de7:2052`).
- v3.264.0 and later offer 2 and accept 1 or 2 (#24458).
- The version 2 shape changed once before v3.264.0: `layout` replaced
  `removeOlds` in v3.262.0 (#24552). No release before v3.264.0 negotiates
  version 2.

## 5. The protocol as the CLI uses it today

This section describes v3.268.0. Each point has an evidence label:

- **source**: read in the CLI source.
- **observed on the wire**: seen in the measured run (section 6). The run
  used v3.250.0 and v3.268.0 with a server that answers 1.
- **unknown**: neither the source nor the run shows it.

### 5.1 Negotiation

- Request field: `journalVersion` (int64, `omitempty`) in
  `StartUpdateRequest` (`sdk/go/common/apitype/updates.go:121-129`). In
  `BeginUpdateRequest` it is a top-level field, because the struct embeds
  `StartUpdateRequest` next to `updateKind` and `program`
  (`updates.go:319-323`). Evidence: source.
- Response field: `journalVersion` (int64, `omitempty`) in
  `StartUpdateResponse` (`updates.go:132-147`), top-level in
  `BeginUpdateResponse` (embedded, `updates.go:328-335`). The comment says it
  "is expected to be less or equal than" the offer, and 0 means disabled.
  Evidence: source.
- The CLI sends the offer for previews too (the client code does not look at
  the kind, `client.go:1618`), but it never journals a preview
  (`backend.go:2210`). Evidence: source.
- The CLI does not check that the answer is at most the offer. It only
  checks for 1 or 2 (`backend.go:1987`). Evidence: source.
- An answer of 1 makes v3.250.0 and v3.268.0 journal. Evidence: observed on
  the wire (measured, see section 6).

### 5.2 Route and request

- `PATCH /api/stacks/{org}/{project}/{stack}/update/{updateID}/journalentries`
  (`client.go:2459`, `client/api_endpoints.go:131`). Evidence: source, and
  observed on the wire (measured, see section 6).
- Headers: `Authorization: update-token <lease token>`
  (`client/api.go:165-166,292-298`, the token value from the update token
  source), `Content-Type: application/json`, `Content-Encoding: gzip`
  (`GzipCompress: true`, `client.go:2460`; `pulumiAPICall` in `api.go`),
  `Accept-Encoding: gzip`, an `Accept` header with the API version, and
  `User-Agent`. Evidence: source. The measurement compared gunzipped request
  bodies, so gzip bodies were observed on the wire.
- Body: `{"entries": [JournalEntry, ...]}` (`apitype.JournalEntries`, end of
  `sdk/go/common/apitype/journal.go`). Evidence: source.
- Response: the CLI passes no response object, so it reads no body. A status
  below 400 counts as success (`client.go:2459`, the 4xx/5xx branch in
  `api.go`). Evidence: source. The status that Pulumi Cloud returns:
  unknown.

### 5.3 Entry fields

Source: `sdk/go/common/apitype/journal.go:89-147,228-230`. Evidence: source.

| JSON name | Type | Notes |
| --- | --- | --- |
| `version` | int | 1, or 2 for kind 10. |
| `kind` | int | 0 begin, 1 success, 2 failure, 3 refresh-success, 4 outputs, 5 write, 6 secrets-manager, 7 rebuilt-base-state, 8 extension-parameterize, 9 snippets, 10 state-migration. An integer on the wire; the names are for logs. |
| `sequenceID` | int64 | Always present. |
| `operationID` | int64 | Always present. 0 for kinds 5, 6, 7, 9 and 10. |
| `removeOld` | *int64 | No `omitempty`: sent as `null` when absent. Index into the base resources. |
| `removeNew` | *int64 | No `omitempty`: sent as `null` when absent. Operation ID of an earlier entry. |
| `pendingReplacementOld` / `pendingReplacementNew` | *int64 | Base index / operation ID. |
| `deleteOld` / `deleteNew` | *int64 | Base index / operation ID. |
| `state` | ResourceV3 | Checkpoint resource, secrets already encrypted. |
| `operation` | OperationV2 | Pending operation for begin entries of create, update, delete, read and import. |
| `isRefresh` | bool | |
| `secretsProvider` | SecretsProvidersV1 | Kind 6. |
| `snippets` | []SnippetV1 | Kind 9. |
| `newSnapshot` | DeploymentV3 | Kind 5: a full deployment. |
| `extensionRef`, `extension` | objects | Kind 8. |
| `requiresByteString` | bool | From v3.254.0. |
| `layout` | []{`baseIndex`?, `stateIndex`?} | Kind 10. |
| `states` | []ResourceV3 | Kind 10. |
| `baseStatePatches` | []{`index`, `state`} | Kind 10. |
| `newStatePatches` | []{`operationID`, `state`} | Kind 10. |

- Which step emits which entry: `pkg/engine/journal_snapshot.go:309-965`.
  The CLI creates the secrets-manager entry when the journal manager starts
  (`journal_snapshot.go:982-997`), so it gets sequence ID 1. Evidence:
  source.
- Secrets stay encrypted in entries. A CLI-side note says that the service
  needs no decrypt step for journal entries (#20716 body). Evidence: source
  (commit text).

### 5.4 Batching

- The engine gives each entry to `cloudJournaler.AddJournalEntry`, which
  serializes it and puts it on a channel
  (`httpstate/journal/snapshot.go:68-144`). Evidence: source.
- One worker collects entries into a batch. It flushes when the batch holds
  `PULUMI_JOURNALING_BATCH_SIZE` entries (default 100), or when the period
  `PULUMI_JOURNALING_BATCH_PERIOD` ms (default 50) passes with a batch that
  is not empty (`snapshot.go:215-278,316-338`; the variable names in
  `sdk/go/common/env/env.go:276-279`). Evidence: source.
- A second goroutine sends flushed batches one after the other, so at most
  one request is in flight. Up to 100 flushed batches can wait in a queue
  (`snapshot.go:228-245`). Evidence: source.
- For the 5-resource program, the CLI sent 8 requests about 60 ms apart in
  batches of 1, 1, 1, 1, 1, 5, 5 and 1 entries. Evidence: observed on the
  wire (measured, see section 6).
- On 413 the CLI splits the batch into two halves and sends each half, down
  to one entry. A single entry that gets 413 fails (`snapshot.go:166-187`).
  Evidence: source. Not measured.

### 5.5 Sequence IDs and order

- Assignment: one atomic counter per update, `Add(1)` when the entry is
  made, so IDs start at 1 and are unique in the update
  (`journal_snapshot.go:82,219-226`). Operation IDs come from a second
  counter, also from 1 (`journal_snapshot.go:77,373`). Evidence: source.
  The run saw `sequenceID` 1 to 16 for 16 entries. Evidence: observed on
  the wire (measured, see section 6).
- The CLI assigns the ID before serialization and before the entry gets to
  the channel. Several engine goroutines can make entries at the same time,
  so the order in a batch can be different from the sequence-ID order
  (`journal_snapshot.go:219-245`, `snapshot.go:117-133`). Evidence: source.
  The CLI has no sort or reorder step: `grep SequenceID` finds only the
  struct, the copy in `SerializeJournalEntry` and the zero check.
- Causal order: an entry that is not elided blocks its step until the server
  accepts its batch (`snapshot.go:143`). An entry that refers to an elided
  entry by `removeNew`, `deleteNew`, `pendingReplacementNew` or a new-state
  patch first waits for the batch result of the elided entry
  (`snapshot.go:68-102`). Thus an entry gets to the channel only after the
  entries it refers to. Evidence: source.
- The replayer of the CLI applies entries in the order it gets them and does
  not sort (`pkg/backend/journal.go:555-569`). The engine comment says that
  replay must apply entries "in the right order"
  (`journal_snapshot.go:73-79`). Evidence: source. The order that Pulumi
  Cloud uses: unknown.
- Validation in the CLI: its local replayer records an error for
  `sequenceID == 0` (`journal.go:632-634`), refuses `version <= 0` or
  `> 2`, refuses kind 10 with `version != 2`, and refuses unknown kinds
  (`journal.go:227-232,336-337`). Evidence: source. What the CLI expects the
  server to validate: unknown. The CLI never reads the journal back.
- CLIs v3.204.0 to v3.209.0 send each entry in its own concurrent request,
  so the arrival order is not the creation order (section 4). Evidence:
  source.

### 5.6 Retries, idempotency and errors

- Retry policy for the journal route: "all methods" (`client.go:2460`).
  Evidence: source.
- Attempts: at most 4 requests (`MaxRetryCount: 4`). The delay is 1 s, then
  times 2, with a limit of 30 s (`client/api.go:436-443`). So the waits are
  1 s, 2 s and 4 s. Evidence: source, and observed on the wire: after a 500
  the CLI sent the same body 3 more times (4 attempts), 1.0 s, 2.0 s and
  4.0 s apart. Each failing batch got its own 4 attempts (measured, see
  section 6).
- Retried: transport errors and 500 to 599
  (`sdk/go/common/util/httputil/http.go:131-143`). From v3.256.0 also 429,
  after a wait for `Retry-After` (limit 30 s) and then the normal delay
  (`http.go:116-129`, #24131). Evidence: source. 500 was observed on the
  wire. 429 and transport errors were not measured.
- Not retried: other 4xx. 401 becomes a login error, 403 a forbidden error,
  429 after the last attempt becomes "request rate-limit exceeded", and other
  codes become an `ErrorResponse` with the `code` and `message` of the server
  (`client/api.go`, `pulumiAPICall`, 4xx/5xx branch). Evidence: source, and
  observed on the wire for 400 and 409: no retry, and the engine fails at
  once (measured, see section 6).
- No fallback: after a journal error, no version sent a `PATCH .../checkpoint`,
  `.../checkpointverbatim` or `.../checkpointdelta` request in the same
  update. Evidence: observed on the wire (measured, see section 6).
- The retry sends the same body again (`http.go:98-105`). If the server
  applied the batch and the answer was lost or was a 5xx, the same entries
  arrive again. Evidence: source. An earlier client comment said "It is safe
  to retry because SequenceNumber serves as an idempotency key" (#20614,
  `client.go@2b1ed7f281`). Whether Pulumi Cloud removes duplicates by
  sequence ID: unknown.
- A failed batch: each waiting entry that is not elided gets the error. The
  step that made it fails through `OnResourceStepPre` or the mutation `End`
  (`pkg/engine/update.go:1313-1326`). The worker continues with the next
  batch, so later batches can still arrive after a refused one
  (`snapshot.go:228-245`). Evidence: source, and observed on the wire: with
  the fault from request 5, the CLI sent the batch with `sequenceID` 6 52 to
  56 ms after the refused batch with `sequenceID` 5 (measured, see
  section 6).
- The update then ends with `complete` status `failed` (`backend.go`, end of
  `runEngineAction`). Evidence: source, and observed on the wire: after 400,
  409 and 500 the CLI sends `POST .../complete` with `{"status":"failed"}`
  and exits with code 1 (measured, see section 6). One exception was
  observed: a refused last batch that holds only an elided entry does not
  fail the update (section 5.8).
- Error text: `error: pre-step event returned an error: [<status>] <message>`
  for a fault at request 2, and `post-step event` and `resource complete
  event` errors for a fault at request 5, then `error: update failed`.
  Evidence: observed (measured, see section 6).
- Durability claim: "When we get a success response, all journal entries are
  guaranteed to be stored safely" (`client.go:2451-2452`). Evidence: source
  (comment).
- A held request: the CLI waited on an open journal request for 299.5 s and
  for 1199.4 s without a retry, without a failure and without a client
  timeout, until the harness stopped it. Evidence: observed on the wire
  (measured, see section 6). A client timeout longer than 20 min: unknown.

### 5.7 Lease and token

- Each journal request gets the current token from the update token source
  (`snapshot.go:166-187`, `client.go:2459-2460`). Evidence: source.
- The token source assumes a 5-minute lease, does a check every 37.5 s
  (duration / 8), and renews with `POST .../update/{id}/renew_lease`
  (`{"duration": 300}`) when less than half the duration is left
  (`pkg/backend/httpstate/state.go:151-158`, `token_source.go:88-128`,
  `client.go:2365-2381`). The request uses the current token. Evidence:
  source.
- During a held journal request, the CLI renewed the lease about every
  187 s: 6 renewals in the 20 min run, the first at about 150 s. Evidence:
  observed on the wire (measured, see section 6).
- A 403 on renewal stops renewal. Later token requests return an error, so
  the next journal request fails (`state.go:129-136`,
  `token_source.go:116-120,150-155`). The CLI tries other renewal errors
  again on the next tick. Evidence: source. Not measured.
- The CLI ignores `tokenExpiration` in the start response (a TODO in
  `state.go:147-150`). Evidence: source.

### 5.8 End of update, cancel, crash

- Normal end: the engine finishes, the journal `Close` waits for all entries
  and the last flush, and always returns nil (`snapshot.go:146-160`). Then
  `POST .../update/{id}/complete` with `{"status": ...}` and the update
  token (`backend.go`, end of `runEngineAction`; `client.go:2471-2485`). No
  journal entry marks the end. Evidence: source.
- An elided entry in the last batch that fails is not reported: its result
  goes only to a promise that nothing reads, and `Close` returns nil
  (`snapshot.go:134-143,146-160`). Evidence: source, and observed on the
  wire: in 3 runs the CLI got a 400 for a last batch that held only entry
  16 (kind 4, outputs), exited 0 and sent `complete` with `succeeded`
  (measured, see section 6).
- Ctrl-C in the running CLI: the engine cancels through the cancellation
  scope. The journal still closes and `complete` follows
  (`backend.go:2245-2354`). Evidence: source. Not measured.
- `pulumi cancel` from another process: `POST .../update/{id}/cancel` with
  the API token of the user, retried for all methods (`client.go:2464-2468`).
  Nothing is specific to the journal. Evidence: source.
- Crash: no more entries, no renewal, no `complete`. Entries that the server
  accepted stay. A replay of those entries turns each begin entry that has
  no matching success or failure, if it carries an `operation`, into a
  pending operation, and keeps the pending `creating` operations of the base
  (`pkg/backend/journal.go:450-469`). Evidence: source (replayer). When the
  harness stopped a CLI that waited on a held request, the CLI sent no
  `complete` and the update stayed `in-progress`. Evidence: observed
  (measured, see section 6). What Pulumi Cloud does with an unfinished
  journal, and when it replays: unknown.
- The next CLI run reads state through the export route or the `deployment`
  of `begin-update` (`backend.go:1896-1900`). Evidence: source.

## 6. Error behavior, measured

### Method

- Base: the spike branch `spike/pulumi-journaling` at commit `8ba6721`
  (`8ba6721a9c99911cc2ae5acdd8590077bc6e9325`).
- Fault switch: commits on the local branch `spike/pulumi-journal-errors`.
  The branch is not merged and not pushed. Commit `c580877` adds the journal
  fault matrix. Commit `b300d4c`, on top of it, adds the existing-stack runs
  and the checkpoint baseline (test `TestPulumiStateSaveErrors` in
  `e2e/state_save_errors_test.go`). Commit `e75159d`, on top of `b300d4c`,
  adds faults on the `complete` call to the same test.
- CLI versions (`pulumi version`): `v3.200.0`, `v3.250.0` and `v3.268.0`
  (`latest` on 2026-10-12, from `https://www.pulumi.com/latest-version`).
  All three archives came from the e2e cache.
- Server: the in-process e2e server, SQLite, fs blobs, journaling on
  (`journalVersion` 1 in the StartUpdate answer). The delta capability was
  off (`DeltaCutoff` 0).
- Program: Pulumi YAML with 5 `pulumi:pulumi:StackReference` resources. The
  state after a good `pulumi up` has 7 resources (1 `pulumi:pulumi:Stack`,
  1 `pulumi:providers:pulumi`, 5 `pulumi:pulumi:StackReference`).
- Each case of `c580877`: a new server and a new stack, then one
  `pulumi up --yes --non-interactive`, then `pulumi stack history --json`
  and `pulumi stack export`.
- Cases of `b300d4c`:
  - Existing stack: a `pulumi up` with no fault creates the 7-resource
    stack. All 5 references point at stack `other`. Then the program points
    `ref-0000` and `ref-0001` at stack `other2` (the 2-resource change), and
    `pulumi up` runs with the fault.
  - Fresh stack: `pulumi up` with the fault on a stack with no state.
  - Journal cases: journaling on, fault 400 from journal request 2, 3, 4
    and 5.
  - Checkpoint cases: `PULUMI_DISABLE_JOURNALING=true` on each CLI call,
    fault 400 and 500 from checkpoint request 2.
  - After the faulted update: `pulumi stack history --json`,
    `pulumi stack export`, then a second `pulumi up` with no fault and a
    second export. "Refs" is the referenced stack in `inputs.name` of
    `ref-0000` and `ref-0001`. "Equals base" compares the normalized export
    with the export after the base update (the normalization of
    `TestPulumiJournaling`: manifest, metadata, times and ids removed).
  - The request log records the `kind` of each journal entry (4 outputs,
    6 secrets manager, 0 begin, 1 success).
- Fault point: `internal/api/pulumi/journal_fault.go` runs at the start of
  the `PATCH .../update/{id}/journalentries` handler, after the lease and
  target checks. It reads environment variables at request time:
  - `DUSTPAN_SPIKE_JOURNAL_FAULT=400|409|500|timeout`.
  - `DUSTPAN_SPIKE_JOURNAL_FAULT_AFTER=<n>`: journal request n of the
    server and all later ones fail. The main matrix uses n = 2.
  - `400`, `409`, `500`: the handler reads the body and answers
    `{"code": <status>, "message": "spike fault: journal entries refused"}`.
    It saves nothing.
  - `timeout`: the handler reads the body and blocks until the request
    context ends (recorded as status 499) or until
    `DUSTPAN_SPIKE_JOURNAL_FAULT_TIMEOUT_MAX` (CLI bound plus 2 min). The
    harness stops the CLI at its bound: 5 min in the matrix, 20 min in one
    extra run.
- Checkpoint fault (`b300d4c`): the same switch also fails
  `PATCH .../update/{id}/checkpoint` through
  `DUSTPAN_SPIKE_CHECKPOINT_FAULT=400|500` and
  `DUSTPAN_SPIKE_CHECKPOINT_FAULT_AFTER=<n>`. The answer is
  `{"code": <status>, "message": "spike fault: checkpoint refused"}`. Each
  route has its own counter. A counter counts only while its fault is set,
  so the base `pulumi up` is not counted.
- `complete` fault (`e75159d`): the switch also fails
  `POST .../update/{id}/complete` through
  `DUSTPAN_SPIKE_COMPLETE_FAULT=400|409|500` and
  `DUSTPAN_SPIKE_COMPLETE_FAULT_AFTER=<n>`, with its own counter. The
  `complete` call of the preview is request 1, so the fault starts at
  request 2, the `complete` call of the real update. The answer text is
  "spike fault: complete refused".
- `e2e/recorder.go` records the start and end time of each request.
  `e2e/journal_errors_test.go` (`TestPulumiJournalErrors`) runs the cases.
  A retry is a request with the same gunzipped body (SHA-256) and the same
  `sequenceID`.
- The raw logs are not in the repository.

Repeat the run with these commands, from a worktree of
`spike/pulumi-journal-errors`:

```sh
export GOTOOLCHAIN=go1.27.2 TMPDIR=$PWD/.loop/tmp
# Main matrix, one go test per version (each under 6 min):
for v in latest 3.250.0 3.200.0; do
  DUSTPAN_EXPERIMENTAL_PULUMI_JOURNALING=1 E2E_PULUMI_VERSIONS=$v \
    go test -tags e2e -count=1 -v -run TestPulumiJournalErrors -timeout 24m ./e2e/
done
# Fault from the 5th journal request:
DUSTPAN_EXPERIMENTAL_PULUMI_JOURNALING=1 E2E_PULUMI_VERSIONS=latest,3.250.0 \
  DUSTPAN_JOURNAL_ERR_CASES=400,409,500 DUSTPAN_JOURNAL_ERR_AFTER=5 \
  DUSTPAN_JOURNAL_ERR_OUT=../.loop/journal-errors-after5 \
  go test -tags e2e -count=1 -v -run TestPulumiJournalErrors -timeout 24m ./e2e/
# Timeout with a 20 min CLI bound:
DUSTPAN_EXPERIMENTAL_PULUMI_JOURNALING=1 E2E_PULUMI_VERSIONS=latest \
  DUSTPAN_JOURNAL_ERR_CASES=timeout DUSTPAN_JOURNAL_ERR_CLI_TIMEOUT=20m \
  DUSTPAN_JOURNAL_ERR_OUT=../.loop/journal-errors-long \
  go test -tags e2e -count=1 -v -run TestPulumiJournalErrors -timeout 24m ./e2e/
# Existing stack and checkpoint baseline (b300d4c), one go test per version
# (each under 2 min):
for v in latest 3.250.0 3.200.0; do
  DUSTPAN_EXPERIMENTAL_PULUMI_JOURNALING=1 E2E_PULUMI_VERSIONS=$v \
    go test -tags e2e -count=1 -v -run TestPulumiStateSaveErrors -timeout 24m ./e2e/
done
# Repeat of the fault at journal request 4, with entry kinds:
DUSTPAN_EXPERIMENTAL_PULUMI_JOURNALING=1 E2E_PULUMI_VERSIONS=latest,3.250.0 \
  DUSTPAN_STATE_ERR_PART=journal DUSTPAN_STATE_ERR_JOURNAL_AFTER=4 \
  DUSTPAN_JOURNAL_ERR_OUT=../.loop/state-errors-kinds \
  go test -tags e2e -count=1 -v -run TestPulumiStateSaveErrors -timeout 24m ./e2e/
# Faults on the complete call (e75159d):
for v in latest,3.250.0 3.200.0; do
  DUSTPAN_EXPERIMENTAL_PULUMI_JOURNALING=1 E2E_PULUMI_VERSIONS=$v DUSTPAN_STATE_ERR_PART=complete \
    DUSTPAN_JOURNAL_ERR_OUT=../.loop/complete-errors \
    go test -tags e2e -count=1 -v -run TestPulumiStateSaveErrors -timeout 24m ./e2e/
done
```

### Results: main matrix (fault from journal request 2)

The columns count only the real update. The preview before it sends no
state. "Journal requests" gives the statuses in arrival order. "Fallback"
means a `PATCH .../checkpoint`, `.../checkpointverbatim` or
`.../checkpointdelta` request in the same update.

| Version | Case | Journal requests (statuses) | Retries (count, spacing) | Fallback route | Exit code | Update result | Stack state (export) |
| --- | --- | --- | --- | --- | --- | --- | --- |
| v3.200.0 | none | 0 | none | `PATCH .../checkpoint` x15 (normal path) | 0 | succeeded | 7 of 7 |
| v3.200.0 | 400 | 0 | none | `PATCH .../checkpoint` x15 (normal path) | 0 | succeeded | 7 of 7 |
| v3.200.0 | 409 | 0 | none | `PATCH .../checkpoint` x15 (normal path) | 0 | succeeded | 7 of 7 |
| v3.200.0 | 500 | 0 | none | `PATCH .../checkpoint` x15 (normal path) | 0 | succeeded | 7 of 7 |
| v3.200.0 | timeout | 0 | none | `PATCH .../checkpoint` x15 (normal path) | 0 | succeeded | 7 of 7 |
| v3.200.0 | disabled | 0 | none | `PATCH .../checkpoint` x15 | 0 | succeeded | 7 of 7 |
| v3.250.0 | none | 8: 200 x8 | none | none | 0 | succeeded | 7 of 7 |
| v3.250.0 | 400 | 2: 200, 400 | 0 | none | 1 | failed | 0 of 7, 0 pending operations |
| v3.250.0 | 409 | 2: 200, 409 | 0 | none | 1 | failed | 0 of 7, 0 pending operations |
| v3.250.0 | 500 | 5: 200, 500 x4 | 3 (same body, seq 2), gaps 1.0 s, 2.0 s, 4.0 s | none | 1 | failed | 0 of 7, 0 pending operations |
| v3.250.0 | timeout | 2: 200, 499 (blocked 299.5 s) | 0 | none | none (stopped at 5 min) | in-progress (no complete call) | 0 of 7, 0 pending operations |
| v3.250.0 | disabled | 0 | none | `PATCH .../checkpoint` x15 | 0 | succeeded | 7 of 7 |
| v3.268.0 | none | 8: 200 x8 | none | none | 0 | succeeded | 7 of 7 |
| v3.268.0 | 400 | 2: 200, 400 | 0 | none | 1 | failed | 0 of 7, 0 pending operations |
| v3.268.0 | 409 | 2: 200, 409 | 0 | none | 1 | failed | 0 of 7, 0 pending operations |
| v3.268.0 | 500 | 5: 200, 500 x4 | 3 (same body, seq 2), gaps 1.0 s, 2.0 s, 4.0 s | none | 1 | failed | 0 of 7, 0 pending operations |
| v3.268.0 | timeout | 2: 200, 499 (blocked 299.5 s) | 0 | none | none (stopped at 5 min) | in-progress (no complete call) | 0 of 7, 0 pending operations |
| v3.268.0 | disabled | 0 | none | `PATCH .../checkpoint` x15 | 0 | succeeded | 7 of 7 |

### Results: extra runs

| Version | Case | Journal requests (statuses) | Retries (count, spacing) | Fallback route | Exit code | Update result | Stack state (export) |
| --- | --- | --- | --- | --- | --- | --- | --- |
| v3.250.0 | 400, from request 5 | 6: 200 x4, 400, 400 | 0 (seq 5 and seq 6 are different batches, 52 ms apart) | none | 1 | failed | 1 of 7 (`pulumi:pulumi:Stack`), 1 pending operation |
| v3.250.0 | 409, from request 5 | 6: 200 x4, 409, 409 | 0 (seq 5, seq 6) | none | 1 | failed | 1 of 7, 1 pending operation |
| v3.250.0 | 500, from request 5 | 12: 200 x4, 500 x8 | 3 for seq 5 and 3 for seq 6, gaps 1.0 s, 2.0 s, 4.0 s each | none | 1 | failed | 1 of 7, 1 pending operation |
| v3.268.0 | 400, from request 5 | 6: 200 x4, 400, 400 | 0 (seq 5, seq 6) | none | 1 | failed | 1 of 7, 1 pending operation |
| v3.268.0 | 409, from request 5 | 6: 200 x4, 409, 409 | 0 (seq 5, seq 6) | none | 1 | failed | 1 of 7, 1 pending operation |
| v3.268.0 | 500, from request 5 | 12: 200 x4, 500 x8 | 3 for seq 5 and 3 for seq 6, gaps 1.0 s, 2.0 s, 4.0 s each | none | 1 | failed | 1 of 7, 1 pending operation |
| v3.268.0 | timeout, 20 min bound | 2: 200, 499 (blocked 1199.4 s) | 0 | none | none (stopped at 20 min) | in-progress (no complete call) | 0 of 7, 0 pending operations |

### Facts from the run

- v3.200.0 never sends a journal request to this server. It sends whole
  checkpoints (`PATCH .../checkpoint`, 15 per update), although the server
  answers `journalVersion` 1. Its source has no `journalVersion` field. The
  faults do not apply to v3.200.0.
- v3.250.0 and v3.268.0 journal to this server: 8 journal requests per
  update for this program, about 60 ms apart. They carry 16 entries
  (`sequenceID` 1 to 16) in batches of 1, 1, 1, 1, 1, 5, 5 and 1 entries.
- No version falls back to a checkpoint, a verbatim checkpoint or a delta
  after a journal error. No case has a `PATCH .../checkpoint*` request after
  a journal error.
- 400 and 409: no retry. The engine fails at once. The CLI may send one more
  journal batch that was already in flight. The runs from request 5 show
  seq 6 sent 52 to 56 ms after the refused seq 5.
- 500: the CLI sends the same body 3 more times (4 attempts in total),
  1.0 s, 2.0 s and 4.0 s apart. Then the update fails. Each failing batch
  gets its own 4 attempts.
- Error text in the CLI output (stdout, Diagnostics), with the status and the
  server message:
  - Fault at request 2:
    `error: pre-step event returned an error: [<status>] spike fault: journal entries refused`,
    then `error: update failed`.
  - Fault at request 5:
    `error: post-step event returned an error: [<status>] ...` and
    `error: resource complete event returned an error: [<status>] ...`, then
    `error: update failed`.
  - stderr was empty in all cases. The output held no token.
- After 400, 409 and 500 the CLI sends `POST .../complete` with
  `{"status":"failed"}`. The exit code is 1. `stack history` shows `failed`.
  The update ends with that call.
- After a refused batch, the export holds only what the accepted batches
  rebuilt: 0 resources after a fault at request 2, and 1 resource
  (`pulumi:pulumi:Stack`) with 1 pending operation after a fault at
  request 5.
- Timeout: the CLI waits on the open request. It has no client timeout of
  5 min or 20 min for this request. It does not retry and does not fail. It
  keeps the lease alive with `POST .../renew_lease` about every 187 s (6
  renewals in the 20 min run). The CLI sends no `POST .../complete`. The
  update stays `in-progress` after the harness stops the CLI.
- With `PULUMI_DISABLE_JOURNALING=true` all three versions use the same
  routes: `POST .../update`, `POST .../update/{id}` (start),
  `PATCH .../update/{id}/checkpoint` x15, `POST .../events/batch`,
  `POST .../complete`, plus `POST .../encrypt` (v3.250.0 and v3.268.0 only)
  and GET reads. No journal request and no delta request (the server hides
  the delta capability in these runs).
- The journal session of the spike server for the update stays in memory
  after a refused batch. The runs of `c580877` did not test a second update
  after the failure. The runs of `b300d4c` did (below).

### Results: existing stack, 2-resource change, fault 400 (`b300d4c`)

Base for each row: 7 resources, refs `other`/`other`, 0 pending operations.
Target: refs `other2`/`other2`. Journal requests give the statuses with the
`sequenceID`s of each batch. For each version, the checkpoint rows follow
the journal rows. "Checkpoint" without a status means fault 400. All journal
rows use fault 400.

| Version | Save path | Fault at | State save requests (statuses) | Retries | Exit | `complete` | Export after the failed update | Second `up`, no fault |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| v3.250.0 | journal | request 2 | 200 [1], 400 [2-10] | 0 | 1 | failed | 7 resources, refs other/other, 0 pending, equals base | exit 0, 7, other2/other2 |
| v3.250.0 | journal | request 3 | 200 [1], 200 [2-10], 400 [11-15], 400 [16] | 0 | 1 | failed | 7 resources, refs other/other, 5 pending operations: base plus the accepted entries | exit 0, 7, other2/other2 |
| v3.250.0 | journal | request 4 | 200 [1], 200 [2-10], 200 [11-15], 400 [16, kind 4] | 0 | 0 | succeeded | 7 resources, refs other2/other2, 0 pending; entry 16 not stored | exit 0, 7, other2/other2 |
| v3.250.0 | journal | request 5 | 4 requests, all 200 (fault not reached) | 0 | 0 | succeeded | 7, other2/other2 | exit 0, 7, other2/other2 |
| v3.250.0 | checkpoint | request 2 | 200, 400 x6 (6 different bodies, 2 to 14 ms apart) | 0 | 1 | failed | 7 resources, refs other/other, 1 pending operation | exit 0, 7, other2/other2, 0 pending |
| v3.250.0 | checkpoint 500 | request 2 | 200, 500 x24 (6 saves x 4 attempts) | 3 per save, 1 s / 2 s / 4 s; 42 s in total | 1 | failed | 7 resources, refs other/other, 1 pending operation | exit 0, 7, other2/other2, 0 pending |
| v3.268.0 | journal | request 2 | 200 [1-7], 400 [8-12], 400 [13] | 0 | 1 | failed | 7 resources, refs other/other, 2 pending operations: base plus the accepted entries | exit 0, 7, other2/other2 |
| v3.268.0 | journal | request 3 | 200 [1], 200 [2-10], 400 [11-15], 400 [16] | 0 | 1 | failed | 7 resources, refs other/other, 5 pending operations: base plus the accepted entries | exit 0, 7, other2/other2 |
| v3.268.0 | journal | request 4 | 200 [1-4], 200 [5-10], 200 [11-15], 400 [16] | 0 | 0 | succeeded | 7 resources, refs other2/other2, 0 pending; entry 16 not stored | exit 0, 7, other2/other2 |
| v3.268.0 | journal | request 5 | 4 requests, all 200 (fault not reached) | 0 | 0 | succeeded | 7, other2/other2 | exit 0, 7, other2/other2 |
| v3.268.0 | checkpoint | request 2 | 200, 400 x6 (6 different bodies) | 0 | 1 | failed | 7 resources, refs other/other, 1 pending operation | exit 0, 7, other2/other2, 0 pending |
| v3.268.0 | checkpoint 500 | request 2 | 200, 500 x24 (6 saves x 4 attempts) | 3 per save, 1 s / 2 s / 4 s; 42 s in total | 1 | failed | 7 resources, refs other/other, 1 pending operation | exit 0, 7, other2/other2, 0 pending |
| v3.200.0 | checkpoint | request 2 | 200, 400 x6 (6 different bodies) | 0 | 255 | failed | 7 resources, refs other/other, 1 pending operation | exit 0, 7, other2/other2, 0 pending |
| v3.200.0 | checkpoint 500 | request 2 | 200, 500 x24 (6 saves x 4 attempts) | 3 per save, 1 s / 2 s / 4 s; 42 s in total | 255 | failed | 7 resources, refs other/other, 1 pending operation | exit 0, 7, other2/other2, 0 pending |

v3.200.0 has no journal rows, because it does not journal (section 4).

### Results: fresh stack, fault at the 2nd state save request

The journal rows come from the main matrix above (`c580877`). The
checkpoint rows come from `b300d4c`. For each version and fault, the
checkpoint row follows the journal row. Expected: 7 resources.

| Version | Save path | Fault | State save requests (statuses) | Retries | Exit | `complete` | Export after the failed update | Second `up`, no fault |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| v3.250.0 | journal | 400 | 200, 400 | 0 | 1 | failed | 0 resources, 0 pending | not run |
| v3.250.0 | checkpoint | 400 | 200, 400 | 0 | 1 | failed | 0 resources, 1 pending operation | exit 0, 7 resources, 1 pending operation stays |
| v3.250.0 | journal | 500 | 200, 500 x4 | 3, 1 s / 2 s / 4 s | 1 | failed | 0 resources, 0 pending | not run |
| v3.250.0 | checkpoint | 500 | 200, 500 x4 | 3, 1 s / 2 s / 4 s | 1 | failed | 0 resources, 1 pending operation | exit 0, 7 resources, 1 pending operation stays |
| v3.268.0 | journal | 400 | 200, 400 | 0 | 1 | failed | 0 resources, 0 pending | not run |
| v3.268.0 | checkpoint | 400 | 200, 400 | 0 | 1 | failed | 0 resources, 1 pending operation | exit 0, 7 resources, 1 pending operation stays |
| v3.268.0 | journal | 500 | 200, 500 x4 | 3, 1 s / 2 s / 4 s | 1 | failed | 0 resources, 0 pending | not run |
| v3.268.0 | checkpoint | 500 | 200, 500 x4 | 3, 1 s / 2 s / 4 s | 1 | failed | 0 resources, 1 pending operation | exit 0, 7 resources, 1 pending operation stays |
| v3.200.0 | checkpoint | 400 | 200, 400 | 0 | 255 | failed | 0 resources, 1 pending operation | exit 0, 7 resources, 1 pending operation stays |
| v3.200.0 | checkpoint | 500 | 200, 500 x4 | 3, 1 s / 2 s / 4 s | 255 | failed | 0 resources, 1 pending operation | exit 0, 7 resources, 1 pending operation stays |

### Facts from the existing-stack and checkpoint runs

- No failed update on an existing stack lost resources. Each export after a
  failed update holds 7 resources. With journaling it holds the base plus
  the entries that the server accepted: the changed references keep the
  base value `other`, and pending operations from accepted begin entries
  appear (0, 2 or 5). With checkpoints it holds the base values plus 1
  pending operation.
- A second `pulumi up` with no fault succeeded in each case on the existing
  stack (exit 0, 7 resources, refs `other2`/`other2`).
- The CLI ignored a 400 on the last journal batch. This occurred in 3 runs
  (v3.250.0 twice, v3.268.0 once). Each time the refused batch held only
  entry 16. The repeat run of v3.250.0 recorded its kind: 4 (outputs). The
  CLI exited 0 and sent `complete` with `succeeded`. The stored state does
  not hold entry 16. This silent success is CLI behavior, on the
  `ElideWrite` path. For an entry marked `ElideWrite`, `AddJournalEntry`
  returns nil at once and puts the send result only into a promise, which
  only a later entry that depends on it reads. `Close` returns nil. Source:
  `pkg/backend/httpstate/journal/snapshot.go@7549635909:128-137` and
  `:141-156` (v3.250.0). The same code is at
  `pkg/backend/httpstate/journal/snapshot.go@128d3f2ec9:133-142` and
  `:146-161` (v3.268.0).
- The fault from journal request 5 did not fire: the 2-resource change sent
  4 journal requests in both versions. The fault from request 4 did not
  fire in the second run of v3.268.0, which sent 3 requests.
- Checkpoint path, 400: the CLI does not retry. The engine continues, and
  each later step tries a new checkpoint: 6 refused saves on the existing
  stack. Checkpoint path, 500: each save gets 4 attempts, 1 s, 2 s and 4 s
  apart, the same spacing as journal batches.
- Error text on the checkpoint path:
  `error: post-step event returned an error: failed to save snapshot: [<status>] spike fault: checkpoint refused`
  (also `pre-step event`), then `error: update failed`. v3.200.0 exits 255.
  v3.250.0 and v3.268.0 exit 1.
- After a failed checkpoint update on a fresh stack, the second
  `pulumi up` succeeded, but its export still holds 1 pending operation.
- The resources of the test program are local. With real cloud resources,
  a failed update can leave resources that exist but are not in the state.
  The checkpoint baseline behaves the same way: after a refused checkpoint
  the engine continued with later steps (6 more save attempts on the
  existing stack), and the export kept the base values `other`/`other`, not
  the target values `other2`/`other2`. A journal refusal at request 2 or 3
  also kept the base values.

### Results: errors on the `complete` call (`e75159d`)

All cases use the existing 7-resource stack with the 2-resource change
(refs `other` to `other2`) and no state save fault. "Checkpoint" means
`PULUMI_DISABLE_JOURNALING=true`. For each version and fault, the checkpoint
row follows the journal row.

| Version | Save path | Fault | `complete` requests (statuses) | Retries | Exit | CLI error text | History of that update | Export after | Next `up`, no fault |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| v3.250.0 | journal | 400 | 400 | 0 | 1 | `error: failed to complete update: [400] spike fault: complete refused` | in-progress | 7 resources, other2/other2, 0 pending | exit 1, `[409] ... another update of this stack is in progress` |
| v3.250.0 | checkpoint | 400 | 400 | 0 | 1 | same, `[400]` | in-progress | 7, other2/other2, 0 pending | exit 1, same 409 |
| v3.250.0 | journal | 409 | 409 | 0 | 1 | same, `[409]` | in-progress | 7, other2/other2, 0 pending | exit 1, same 409 |
| v3.250.0 | checkpoint | 409 | 409 | 0 | 1 | same, `[409]` | in-progress | 7, other2/other2, 0 pending | exit 1, same 409 |
| v3.250.0 | journal | 500 | 500 x4 | 3, 1 s / 2 s / 4 s | 1 | same, `[500]` | in-progress | 7, other2/other2, 0 pending | exit 1, same 409 |
| v3.250.0 | checkpoint | 500 | 500 x4 | 3, 1 s / 2 s / 4 s | 1 | same, `[500]` | in-progress | 7, other2/other2, 0 pending | exit 1, same 409 |
| v3.268.0 | journal | 400 | 400 | 0 | 1 | same, `[400]` | in-progress | 7, other2/other2, 0 pending | exit 1, same 409 |
| v3.268.0 | checkpoint | 400 | 400 | 0 | 1 | same, `[400]` | in-progress | 7, other2/other2, 0 pending | exit 1, same 409 |
| v3.268.0 | journal | 409 | 409 | 0 | 1 | same, `[409]` | in-progress | 7, other2/other2, 0 pending | exit 1, same 409 |
| v3.268.0 | checkpoint | 409 | 409 | 0 | 1 | same, `[409]` | in-progress | 7, other2/other2, 0 pending | exit 1, same 409 |
| v3.268.0 | journal | 500 | 500 x4 | 3, 1 s / 2 s / 4 s | 1 | same, `[500]` | in-progress | 7, other2/other2, 0 pending | exit 1, same 409 |
| v3.268.0 | checkpoint | 500 | 500 x4 | 3, 1 s / 2 s / 4 s | 1 | same, `[500]` | in-progress | 7, other2/other2, 0 pending | exit 1, same 409 |
| v3.200.0 | checkpoint | 400 | 400 | 0 | 255 | same, `[400]` | in-progress | 7, other2/other2, 0 pending | exit 255, same 409 |
| v3.200.0 | checkpoint | 409 | 409 | 0 | 255 | same, `[409]` | in-progress | 7, other2/other2, 0 pending | exit 255, same 409 |
| v3.200.0 | checkpoint | 500 | 500 x4 | 3, 1 s / 2 s / 4 s | 255 | same, `[500]` | in-progress | 7, other2/other2, 0 pending | exit 255, same 409 |

The full 409 text of the next `up` is
`error: [409] start update <update id> of acme/e2e-project/dev: another update of this stack is in progress`.

### Facts from the `complete` runs

- The CLI retries `complete` only on 500: 4 attempts, 1 s, 2 s and 4 s
  apart. It does not retry 400 or 409. Journaling on or off makes no
  difference.
- All state saves succeeded before `complete`. The export holds the target
  state (7 resources, `other2`/`other2`, 0 pending operations).
- The CLI exits with an error (1, or 255 for v3.200.0), although the
  resources and the state are complete.
- The update stays `in-progress` in `pulumi stack history`. The next
  `pulumi up` fails at once with the 409 "another update of this stack is
  in progress" of the server. The runs did not test `pulumi cancel` or
  lease expiry after this state.
- Link to R37: if the server refuses a `complete` with `succeeded` on an
  update that has a sequence gap, the stack stays blocked until the update
  is canceled or its lease expires. Thus R37 needs a defined way out
  (cancel or lease expiry). This run did not test either one.

### Limits

- One run per case, except one repeat of the fault at journal request 4
  on the existing stack (v3.250.0 and v3.268.0). Each timing gap is from
  one run.
- One program with 5 resources. The batch count and the point where the
  fault hits depend on the program.
- The CLI batches by time, so the batch split changes between runs of the
  same case.
- On the existing stack, the fault from journal request 5 never fired: the
  2-resource change sends only 4 journal requests. The fault from request
  4 did not fire in the repeat run of v3.268.0, which sent 3 requests.
- The delta capability was off (`DeltaCutoff` 0) in all runs. No run had
  the delta capability on.
- No follow-up update after a journal fault on a fresh stack, or after a
  timeout. The `b300d4c` runs made a second update in all other cases.
- The longest timeout bound was 20 min. The timeout cases did not reach a
  CLI-side timeout. A bound longer than 20 min was not tested.
- The server answered `journalVersion` 1 only. Version 2 was not measured.

## 7. Requirements

These are the requirements for a server that answers a non-zero
`journalVersion`, from the facts above. Each has a mark (must, should or
not needed) and a source. The numbers are stable: later ADRs and issues can
cite them, and a number is not used again. The lists below keep the
numbers and put each requirement under the negotiated version it applies to.

### Version 1 and version 2

Negotiation:

- **R1** (must). Accept `journalVersion` (int, optional) in the start-update
  body and, if the server lists `begin-update` v1 in `/api/capabilities`,
  in the `begin-update` body. Answer `journalVersion` in the same place.
  Source: section 5.1.
- **R2** (must). Answer 0 (or omit the field) to keep checkpoints. Answer
  only a version that the CLI offered. An answer that the CLI does not
  accept (2 to a CLI that offers 1; 3 or more to any CLI) keeps checkpoints
  with no message. Source: `backend.go@7549635909:2068`,
  `backend.go:1982-1988`.
- **R3** (must). Keep all checkpoint routes. CLIs before v3.204.0, CLIs
  v3.204.0 to v3.224.0 without `PULUMI_ENABLE_JOURNALING`, CLIs with
  `PULUMI_DISABLE_JOURNALING`, and previews never journal. Source: section
  4; measured, see section 6 (v3.200.0 and the `disabled` cases).

Route and transport:

- **R6** (must). Serve
  `PATCH /api/stacks/{org}/{project}/{stack}/update/{updateID}/journalentries`
  for all update kinds (the segment is always `update`). Source: section
  5.2, `client.go:540-543`.
- **R7** (must). Authorize with `Authorization: update-token <token>` from
  the start or renew response. Source: sections 5.2 and 5.7.
- **R8** (must). Accept request bodies with `Content-Encoding: gzip`.
  Source: section 5.2.
- **R9** (must). Accept `{"entries": [...]}` with 1 to N entries. N is 100
  by default, but the CLI variable can make it larger. v3.204.0 to v3.209.0
  send one entry per request, possibly at the same time. Source: sections
  4 and 5.4.
- **R10** (should). Answer 413 when a batch is too large. The CLI splits it.
  Source: section 5.4.
- **R11** (must). Answer 2xx only after the entries are stored durably. The
  CLI treats success as "stored safely" and continues. Source: section 5.6.
- **R12** (must). Make a retried batch safe. The CLI sends the same body
  again after a 5xx, a transport error, or (from v3.256.0) a 429, up to 4
  attempts. To apply a success entry again appends a second resource, so the
  server must find entries that it already has, for example by
  `sequenceID`, which is unique in the update. Source: section 5.6,
  `journal.go:239-246`; measured, see section 6 (500 cases).
- **R13** (should). Answer 429 with `Retry-After` for rate limits only for
  v3.256.0 and later. Older CLIs fail the step on 429. Source: section 5.6.

Entry validation and order:

- **R14** (must). Refuse an entry with `version <= 0` or with a version
  above the negotiated one. Refuse kind 10 unless `version == 2`. Refuse
  unknown kinds, do not ignore them. Source: `journal.go:227-232,336-337`
  (CLI replayer rules from #24327); section 3 (the rows for kinds 8 and 9
  show the cost of ignoring a kind).
- **R15** (must). Accept `null` for `removeOld` and `removeNew`. Source:
  section 5.3.
- **R16** (must). Apply each entry after all entries that it refers to.
  Sequence-ID order, and the order of arrival from v3.210.0 on, both meet
  this (section 5.5). For v3.204.0 to v3.209.0 the order of arrival does not
  (concurrent requests), so a server that supports those versions must
  order by `sequenceID` or wait for missing IDs. The order that Pulumi Cloud
  uses is unknown. Source: section 5.5.
- **R17** (should). Treat `sequenceID == 0` as an error. Source:
  `journal.go:632-634` (#20611).

Replay (match `pkg/backend/journal.go@2255563bcc`):

- **R18** (must). Start from the checkpoint that the CLI started from. The
  `*Old` fields are indices into the `resources` list of that checkpoint, so
  the server must keep the exact resource order it served. Source:
  `journal_snapshot.go:350-366`, section 5.8 (state read).
- **R19** (must). Kind 0 begin: record the entry as incomplete by
  `operationID`. Kind 2 failure: drop the incomplete entry. Source:
  `journal.go:237-240,286-287`.
- **R20** (must). Kind 1 success: drop the incomplete entry; append `state`
  to the new list and map `operationID` to its index; mark `removeOld` for
  removal from the base and `removeNew` for removal from the new list; set
  `delete: true` on the base resource at `deleteOld` and on the new resource
  at `deleteNew`; set `pendingReplacement = true` on the base resource at
  `pendingReplacementOld` and on the new resource at
  `pendingReplacementNew`; note `isRefresh`. Source: `journal.go:239-268`.
- **R21** (must). Kind 3 refresh-success: drop the incomplete entry; note a
  refresh. `removeOld` with no `state` removes the base resource, and with
  `state` replaces it. The same for `removeNew` in the new list. Source:
  `journal.go:269-285`.
- **R22** (must). Kind 4 outputs: replace the base resource at `removeOld`,
  or the new resource at `removeNew`, with `state`. Source:
  `journal.go:288-294`.
- **R23** (must). Kind 5 write: replace the base with `newSnapshot`. The CLI
  sends it before other mutations. Source: `journal.go:295-298`,
  `journal_snapshot.go:424-456`.
- **R24** (must). Kind 6 secrets-manager: set the base `secretsProviders`
  unless type and state are equal. Source: `journal.go:299-310`.
- **R25** (must). Kind 7 rebuilt-base-state: build a deployment from the
  current state, make it the base, and clear each accumulated map and list
  (extensions too). Source: `journal.go:313-329`.
- **R26** (must, version 1 from v3.249.0, and version 2). Kind 8
  extension-parameterize: store `extension` under `extensionRef`. Kind 9
  snippets: replace the base `snippets`. Source:
  `journal.go:311-312,330-331`; section 3, rows #23564 and #23650.
- **R27** (must). Build the deployment as `GenerateDeployment` does: new
  resources that are not removed, in order, then base resources that are not
  removed (with replacements, pending-replacement and delete marks); pending
  operations from incomplete entries that carry `operation`, plus the
  `creating` operations of the base; after a refresh, `rebuildDependencies`
  (drop dangling dependencies, property dependencies and `deletedWith`,
  prune `replaceWith`, undangle parents); the secrets providers, metadata
  and snippets of the base; a new manifest; extensions from the journal
  merged with those of the base (base values win for the same key); then
  `NormalizeURNReferences()`. Source: `journal.go:342-517`; section 3, rows
  #20683, #23927 and #21578.
- **R28** (must). Compute deployment features and the schema version as
  `ApplyFeatures` does. Add `byteString` when an applied entry has
  `requiresByteString` (v3.254.0 on), and `snippets-prototype` when the
  deployment has snippets. Use the latest schema version when a feature is
  set. Source: `journal.go:404-505`,
  `pkg/resource/stack/deployment.go:160-185`.
- **R31** (not needed). Decryption of secrets for replay. Entries carry
  ciphertext and the replayer copies it. Source: #20716 body,
  `journal.go:44-168`.

Lease, end and failure:

- **R32** (must). Keep `renew_lease` working during a journaled update. The
  CLI renews about every 2.5 to 3 minutes and stops after a 403. Source:
  section 5.7; measured, see section 6 (about every 187 s during a held
  request).
- **R33** (must). Make the checkpoint from the journal before the next CLI
  run reads state (export or the `deployment` of `begin-update`). Whether
  Pulumi Cloud replays per batch or at `complete`: unknown. Source: section
  5.8.
- **R34** (must). After a crash, make the accepted entries the state, with
  unfinished operations as pending operations. Source: section 5.8,
  `journal.go:450-469`. A related case was measured: after a refused batch,
  the export held what the accepted batches rebuilt, with 1 pending
  operation after a fault at request 5 (measured, see section 6).
- **R35** (should). Define what happens to batches that arrive after the
  server refused one. The CLI continues to send them. Source: section 5.6;
  measured, see section 6.
- **R36** (not needed). An end-of-journal marker. The CLI sends none. The
  end is `complete`. Source: section 5.8.
- **R37** (must). Record each refused or unstored batch of an update. A
  `complete` with status `succeeded` on an update that has a sequence gap
  or a refused batch must not end as `succeeded`. The CLI can ignore a
  refusal of its last batch when that batch holds only elided entries: it
  exits 0 and sends `succeeded`, and the state does not hold the refused
  entries. Source: measured, section 6.

### Version 1 only

- **R4** (should). To journal with v3.204.0 to v3.263.0, answer exactly 1.
  Source: section 4.
- **R30** (not needed). Kind 10 handling. A CLI never sends it unless 2 was
  negotiated. Source: `pkg/engine/journal_state_migration.go:47-50`.

### Version 2 only

- **R5** (should). To let v3.264.0 or later run state migrations while it
  journals, answer 2. An answer of 1 makes the CLI refuse migrations
  (checkpoints allow them). Source: section 4 (v3.268.0, state migrations).
- **R29** (must). Kind 10 state-migration: refuse it if the base has pending
  operations or if an incomplete entry carries an `operation`; check
  `layout`, `states`, `baseStatePatches` and `newStatePatches` as
  `applyStateMigration` does; build the new base from `layout`; map the
  stored base indices to the new positions; apply new-state patches by
  operation ID; refuse the entry if the result fails the integrity checks;
  keep the state unchanged on each refusal. Source:
  `pkg/backend/journal_state_migration.go:27-211`.

## 8. Open questions

From the source:

1. Order: does Pulumi Cloud apply entries by `sequenceID` or by arrival?
   The two give a different resource order in the checkpoint for
   independent resources (RFC 62 also saw order differences). The CLI source
   does not say.
2. Idempotency: does Pulumi Cloud remove duplicates by `sequenceID`, by
   `(sequenceID, operationID, kind)` (the key that the spike uses), or not at
   all? A comment in #20614 suggests that the sequence number is the
   idempotency key, but that comment was on a removed route.
3. Gaps: does the server refuse a batch whose sequence IDs skip numbers, or
   does it wait? The CLI can make gaps when a batch fails and later batches
   still go out (section 5.6), and v3.204.0 to v3.209.0 send concurrently.
4. Refusal codes: which 4xx does Pulumi Cloud send for a bad entry, an
   unknown kind, or a version that it did not negotiate? The CLI does not
   retry these and fails the step (section 5.6). This answers the RFC 62
   question "what does the CLI do on 400": it fails the step, does not
   retry, does not fall back to checkpoints, and later batches can still
   arrive (measured, see section 6).
5. Lease check: does Pulumi Cloud refuse journal writes after the lease
   expires, and with which status? The CLI makes only a 403 on renewal into
   a stop (section 5.7).
6. Replay time: does Pulumi Cloud build the checkpoint again after each
   batch, at `complete`, or on read? The source shows only that the next CLI
   run reads through export or `begin-update`.
7. Version 1 drift: CLIs v3.249.0 and later send kinds 8 and 9, and v3.254.0
   and later send `requiresByteString`, under version 1. How did Pulumi
   Cloud handle these before its replayer knew them? Unknown. The service
   code is private.
8. Version 2 on the server side: #24458 says "Needs pulumi-service#49631".
   That pull request and its acceptance rules are not readable.
9. Maximum sizes: the source does not give the request size limit of Pulumi
   Cloud that causes 413, or a limit on entries per update.
10. The `begin-update` deployment: the CLI accepts a missing `deployment`
    and then gets it (#24417). Whether the served deployment must be equal
    to the export byte for byte, to keep indices stable, follows from R18
    but the source does not state it.
11. Lost elided entries: a failed last batch of elided entries is not
    reported (section 5.8). Whether Pulumi Cloud repairs such a journal, or
    whether the loss has no effect because elided entries carry no
    meaningful change (#21576 body), is not settled for each elided kind.
    The secrets-manager entry is also elided (`journal_snapshot.go:325-330`).
    The measurement saw one such loss: entry 16, kind 4 (outputs), was not
    stored (section 6). What that entry would have changed in the state was
    not measured.
12. Kind count: future kinds may again arrive under version 1, as kinds 8
    and 9 did. The source has no rule that a new kind needs a new version.
    Only kind 10 got one.

From the limits of the measurement (section 6):

13. Timing spread: each case ran once, except one repeat. The retry gaps,
    the batch spacing and the batch split can be different in other runs.
14. Other programs: the batch count and the point where the fault hits
    depend on the program. Only one 5-resource program was measured.
15. Client timeout: does the CLI time out a held journal request after more
    than 20 min? The run did not reach a CLI-side timeout.
16. Delta capability: does the CLI behave the same on a journal error when
    the server also offers delta checkpoints? All runs had it off.
17. Follow-up update: on an existing stack, a second `pulumi up` succeeded
    after each failed update (section 6). What does the next `pulumi up` do
    after a journal fault on a fresh stack, or after a stopped (timeout)
    update? No run tested these.
18. Version 2 errors: the run measured version 1 only. The error behavior
    with version 2 negotiated is not measured.
19. `complete` refused, then `pulumi cancel` and lease expiry: after the
    server refuses `complete`, the update stays `in-progress` and the next
    `pulumi up` fails with 409 (section 6). Does `pulumi cancel` end such an
    update, and does lease expiry free the stack? No run tested these. R37
    depends on this way out. How Pulumi Cloud handles a refused batch
    followed by `complete` with `succeeded` is unknown.
20. Pending operation after a checkpoint fault on a fresh stack: after the
    failed update, the second `pulumi up` succeeded, but its export still
    held 1 pending operation (section 6). The measurement does not show why
    the pending operation stays.

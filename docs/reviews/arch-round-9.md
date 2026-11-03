# Architecture review, round 9

Range: main `6c35afa` (end of round 8) to `b72ef46` (trains 38 to 44 and #2293). Reviewed on 2026-11-03.

Scope:

- the one baseline per dialect, the SHA-256 content check and `opendustpan dev reset` (ADRs 322 to 324, #2293);
- the restore claim renewal, the pre-backup on the restore and the restore round-trip cost model;
- sshsig ECDSA P-256;
- the final state version for triggers;
- the writer gate test;
- the safe cause and the request ID on the 500 log line;
- the sign-in method designs (ADRs 320 and 321);
- the swarm pull requests.

Method:

- `git diff 6c35afa b72ef46` by area.
- A read of each changed port, both store adapters and the `dev reset` command.
- `rg` over `docs/adr` and `docs/rfc` for each decision.
- A check of the disabled tests against #2305.

Each finding below was confirmed against the code at `b72ef46` unless it is marked "plausible".

## Findings of severity major

| # | Finding | Issue |
| --- | --- | --- |
| 1 | The checks that #2305 disables skip with no condition (`pkg/store/{sqlite,postgres}/frozen_test.go:33`, `pkg/store/internal/baseline/roundtrip_test.go:46,75`). `scripts/regenerate-baseline.sh:38-43` refuses a collapse only when `testdata/tag-migrations/` is filled by hand. It does not check for a Git tag, against ADR 322 ("The script refuses a collapse when a tag exists"). After the first tag, an in-place edit or a collapse of the shipped baseline makes the content check refuse every user database, with no migration path. One-way door. | #2335 |
| 2 | `inventory.Entry.LastRelease` (`pkg/inventory/inventory.go:43-47,73-83`, b3ba0877) is a field that only OpenTofu fills. It is in the shared core, the public SDK and the tool-neutral unit view (`internal/api/inventory/inventory.go:315-317`). ADR 298 puts it in "the API of the workspace". This breaks the shared-core rule (ADR 12, ADR 13). | #2336 (related #2229) |
| 3 | The sshsig ECDSA layers (`internal/sshsig/sshsig.go:319-329,603,619`, 712bb057) have no test of their own. No test uses a hand-built wrong-curve, nil or off-curve key, and the range-check cases cannot tell that layer from `VerifyASN1`. The code has no on-curve check and no `ssh-keygen` P-256 vector. | #2337 (related #2172) |
| 4 | The restore swap still cannot outlast the 30 s lease (plausible at scale, not reproduced). After 9abbcd80 and 48957755, the window in which no renewal can write runs from `LOCK TABLE` in `keepControlTables` (`pkg/store/postgres/restore.go:368-394`) to the commit. It grows with the durable history, which has no retention. A takeover does not count toward `MaxAttempts`, so a large installation can repeat the swap in maintenance mode with no end. On SQLite the whole swap blocks renewal. | #2283 (existing; evidence added) |

## Findings of severity minor

All in #2338, 29 items:

- **dev reset:** it releases the guard between the kind check and the move; it deletes the previous copies first and is not atomic; `checkDevOwner` passes when the owner cannot be read; more Windows-only code; a settings layer on the audit change of a reset.
- **ADR text:** ADR 323 says the version test skips "with a TODO", and it does not.
- **Design-only suites:** the conformance suites of ADRs 320 and 321 ignore their fixture, so a wired adapter would pass and test nothing.
- **Writer gate:** its exemption lists can grow, and it has several bypasses.
- **Triggers and logging:** the final version reader is wired by a type assertion; `Dispatcher.Upstream` has no production caller (plausible); `io.EOF` is labeled `unexpected_eof`; constants are duplicated after the move of `RebindSecretsProvider`.
- **Restore claim:** `peekStep` copies `checkStep`; the claim SQL is copied; `RunBeforeRestore` hides an `ErrInvalid`; `StartBeforeRestore` is used only by tests; more service logic in `pkg/backup`.
- **Jobs and inventory:** no caller handles `ErrUnitGone`; the cursor commit claims more than it does; the hidden-character checks are copied and differ.
- **Registry:** the pre-promote check exists for providers only.
- **Smaller fixes:** the clientgen kill race; `rollbackRead` hides the error; the artifact sweep `Capped` can be wrong; a long lock reason can still cut its removal route; the source filter shows no effective value; the argon2id waiter list has no length limit.
- **#2331 item 1:** not live on main yet.

## Checked and clean

- **Layering:** no domain package changed in the range imports `net/http`, `database/sql`, a driver, `pkg/store/...` or `internal/api/...`. This covers job, trigger, inventory, savedview, tempadmin, unitimport, workspace, localuser, argon2id, session, registry, backup and durable. The new `job.FinalVersionReader` port is in `pkg/job`, its adapter in `pkg/store/internal/jobstore`, and its cases in `jobtest/finalversion.go`.
- **Shared core:** apart from finding 2, no tool-specific field was added to a shared package. No adapter under `internal/api/<tool>` imports another. `RebindSecretsProvider` left `pkg/stack` for the Pulumi adapter (04aa2f02), which closes part of #1584.
- **Content check:**
  - `migrate.Check` and each adapter's `verifyRecorded` compare the name and the SHA-256 on their own. Each refuses a value that is not 64 lowercase hex digits, and a version table with no `sha256` column.
  - The defense-in-depth comments are in place.
  - The hash file of `0168_baseline.sql` matches the file.
- **dev reset never runs on its own:** the only caller is the `dev reset` subcommand (`cmd/opendustpan/dev.go:153`). `serve` never offers a reset (`schemaRefusal`, `cmd/opendustpan/devreset.go:340-363`). `dev reset` refuses a database of `serve`, one with no kind, one with an unknown kind, a hard link, a linked directory, a linked `blobs` directory and a live server. It writes a WARN line and an audit entry of `@server`.
- **Disabled checks:** the unconditional `t.Skip` calls added in the range are exactly the five that #2305 lists, plus the design-only suite cases of ADRs 320 and 321 (minor item 7).
- **500 log line:** the cause comes from a closed set of constants, never from `err.Error()`. The request ID passes a 1-to-64 lowercase hex allowlist. CR and LF are tested. No new catalog code is needed.
- **Trigger final version:** a reader error or a missing reader fails closed with `ErrVersionMismatch`.
- **Restore claim:**
  - The pre-backup start claims only the operation that it created, in the same transaction.
  - The takeover of a claimed pre-backup is tested.
  - A stale restore holder cannot pass `MarkRestoreApplying`.
  - The expiry uses the database clock.
  - The cost-model change (b4bc017e) is test-only, and the restore still compares the schema version only (#2306).
- **Swarm security fixes:**
  - The compare of a locked name runs with one audited refusal and its own timing test (0cd08f2f).
  - The argon2id FIFO order frees the memory when a grant and a cancel happen together (fc3891eb, bb87a4b1).
  - The action is checked before the output name lookup (60afac73).
- **ADRs 320, 321 and 323:** each "Changes" and "Changed by" pair is in place (ADRs 55, 70, 227, 264, 275, 285). The ports and the errors that they name exist.

## The finding that matters most

Finding 1 (#2335). Until the first tag, editing the baseline in place is the rule. Once the first tag exists, the same edit refuses every user database, and the code has no path back. Today, only manual release steps stop that edit: #2305, a hand-run copy script, and a guard that looks at a test-data directory instead of the tag. Each safeguard should turn on by itself from one fact that the tree holds, before the first tag goes out.

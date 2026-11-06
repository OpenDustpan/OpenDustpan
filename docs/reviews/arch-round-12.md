# Architecture review 12

Range: `b4e1757..76073e03` on `main` (trains 65 to 69, 40 commits).

Scope: the passkey store, the go-webauthn adapter, `internal/mfasvc` and the
passkey routes; the GitHub forge review requests, the FindCommit trust checks,
the tree-entry moved-head check and the sealed write-back proof; the Expr
evaluator adapter; the runner session routes and the production job recorder;
`internal/signinsvc`; the manual lock authorizer chain fix; the e2e change to
API-only in-process servers.

The weekly usage was at the warning level, so this round looks at the large
risks only. It reads the code at `76073e03` and the ADRs in `docs/adr`.

## Findings

| Severity | Finding | Issue |
| --- | --- | --- |
| major | A refused runner session exchange, or a failed proof for a known session, writes no audit entry. ADR 133 says that the audit log records the refused exchange. `EventHandoffRefused` is declared and never written. | #2479 |
| major | The production job recorder uses the default retention (400 days). `--plan-retention` and `--job-log-retention` from ADR 152 are declared in the settings table but the binary does not register or read them. Each log stores its expiry at creation, so a later fix does not change the logs written before. | #2480 |
| major (known) | The write-back proof binds the repository, the trailer and the base, but not the content of the changes. A copied proof line can mark other content as the delivery of the job. | #2469 (existing) |
| minor | Ten items: no conformance suite for `mfa.RelyingParty`; passkeys turn off with a log line only; the passkey begin limiter; an unaudited read error in `mfasvc`; no parent check in `deliveryIn`; the proof as an AEAD seal; `DeleteBranch` without a head check; the Expr Limiter doc; e2e worker coverage; the dynamic authorizer chain test. | #2481 |

## Notes by area

### Passkeys (e53d529, bff0d60, 3f76f0f)

- The service binds each challenge to its kind, person, pending sign-in and
  session, takes it once by its digest, and refuses a ceremony of another
  person before it calls the relying party. Each refusal writes an audit
  entry with the cause. The store enforces the limit of 20 passkeys and the
  unique credential ID in the schema too. These layers are independent.
- The routes need the console session of a person, and the writes need a
  fresh sign-in. The handler checks the person again as its own layer.
- The guide says that a passkey does not sign anyone in yet and is not a
  second factor yet. The sign-in service does not read the passkeys. This is
  consistent.
- The `RelyingParty` port has no conformance suite (minor, #2481).

### Forge (482ee5c to 12ae46f)

- `trustedCommit` states plainly that the author and committer checks are not
  independent layers against a push, and that only `checkVerified` stops it.
  The proof was added as its own layer. It does not stop a replay of a proof
  line with other content (#2469). FindCommit has no production caller yet
  (the write-back executor is not built), so the gap is not reachable today.
  Fix it before the executor lands, and before a tag ships proofs in
  repositories.
- The moved-head check now compares mode and object ID of each tree entry and
  fails closed on any entry that is not a regular file. Good.
- `Prover` lives in `pkg/forge/github` while the port doc of `Commits`
  requires a proof from every adapter. #2469 already tracks the move.

### Expr evaluator (8fb4857 to 732c10c)

- Strong bounds: source size, node count, allowlist of builtins, no `let`, no
  range, nested closures limited, a memory budget, a metered work budget for
  string reads, a timeout, and a shared Limiter for runs that passed their
  time limit. The adapter runs the `catalogtest.RunEvaluator` suite.
- The adapter is not wired into the server yet; one doc comment says it is
  (minor, #2481).

### Runner session routes and the job recorder (9d94f58 to 31354b4)

- The exchange checks the session challenge signature, the proof, and the
  nonce before the store checks the token in its own transaction. The step,
  log and finish routes check the claim owner and token, and the store checks
  the fence. `EndStepChecked` compares the closed log with the launcher's
  count and hash.
- Refusals are not audited (#2479).
- The recorder uses fixed retention periods (#2480).

### Sign-in service (587575b, c0bab65)

- One order of steps for every method, and the local password has its own
  entry that checks the generation. `isTarget` checks the principal again as
  its own layer. Each refusal writes one audit entry. No finding.

### Manual lock authorizer (f76d791 to 0ddd195)

- The fix builds the auth ports before the services and passes the wrapped
  authorizer. The new test checks each recorded `Authorize` call against a
  named list of bare callers. The test is dynamic; a static check would also
  catch a path that the test does not call (minor, #2481).

### e2e API-only in-process servers (6e44515, 3d5d46c)

- The reason is sound (the claim-loop readiness check). The worker is now
  covered only by the binary environments (minor, #2481).

## Checks against the review list

- One-way doors: the proof format in commit messages (`Dustpan-Proof`, purpose
  `v1`) stays in Git history. Settle #2469 and the key rotation rule before a
  tag. The job log expiry is fixed at creation (#2480).
- Shared core: no tool-specific field entered a shared package in this range.
- Layering: domain packages import no HTTP, SQL or store packages in this
  range.
- Error codes: the passkey and runner answers use codes from the catalog.

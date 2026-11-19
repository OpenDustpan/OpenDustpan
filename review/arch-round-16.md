# Architecture review, round 16

Range: `a39ce4470d33..4a78446d11cb` of `main` (33 commits, 140 files).
Scope: the code commits (#3535, #3558, #3565, #3572, #3573, #3576, #3583, #3586, #3620, #3671), RFC 133, and the OpenSpec changes in the range (read at the level of their ADR checks and "Open questions" sections).

## Findings

| # | Severity | Issue | Where | ADR / rule |
|---|----------|-------|-------|------------|
| 1 | High | #3680 | `pkg/backup/service.go:1112`, `pkg/store/internal/blobrefs/blobrefs.go:254`, `internal/registry/service/service.go:36-42` | ADR 250 (defense in depth, step 1 and step 3); follow-up of #3545 |
| 2 | Medium | #3681 | `pkg/dustpan/localusers.go:264`, `internal/localusersvc/service.go:310`, `pkg/dustpan/signin.go:80-86` | CLAUDE.md "No magic" (effective values, a true "why"); follow-up of #3544 |

### 1. A publish can put a namespace above the registry limits of a backup (#3680)

The fix of #3545 moved the namespace limit of the registry files from the restore to the backup. Publish still checks only one file (512 MiB) and one release (2 GiB), and `Config.MaxArchiveBytes` / `MaxReleaseBytes` can raise them. A publisher in one namespace can publish two releases of about 1.1 GiB. After that, every backup of the installation fails with `backup.registry-files-too-large`, for all namespaces, until an operator removes releases. The boundary that accepts the data (publish) does not apply the limit; the backup is now the first layer. #3545 ended with "Decide if publish must also cap the namespace total"; the fix did not decide it.

Proposed fix: one source for the constants, a namespace total at publish, no override above the backup limits, and keep the backup check as a second layer.

### 2. The public URL is named for every cause of a missing console sign-in (#3681)

`NoPublicURL: signIn == nil`. `newSignIn` returns nil also when the store keeps no sessions, keeps no providers, or has no session policy service. For those causes the policy shows `enabledSource: "public-url"`, the API answers "start the server with --public-url", and the audit log records the same reason, also when a valid https public URL is set.

Proposed fix: return the cause of a nil sign-in and pass it to the local-user service as the source.

## Checked, no new finding

- **mTLS exchange token (#3576).** The new kind is in `Kind.Valid` and `FromExchange`. `CheckExchange` and `ExchangeKindNeedsBundle` fail closed on an unknown kind. The recheck of the rule match and of the active bundle version runs in the fenced, audited transaction in both adapters (`recheckExchange`, `pkg/store/postgres/trustrules.go:346`, `pkg/store/sqlite/trustrules.go:363`). The audited transaction holds the namespace row, so a bundle rotate in the same namespace cannot commit between the recheck and the token insert. Conformance cases were added in `trustruletest`. The open #3548 and #3551 (trust bundle authorization and audit) are not touched by this commit.
- **Proxy credential on an endpoint change (#3572, fix of #3546).** The service clears the credential and writes `credentialCleared` and `reason` in the audit entry. Both stores refuse a write that keeps the stored credential for another endpoint. `UpstreamEndpointChanged` fails closed on a URL that does not parse. Conformance cases in `runnertest`.
- **Runner pool field errors (#3565).** `FieldError` holds fixed text only, so no caller input reaches the answer.
- **Job token store wiring (#3558, fix of #3540) and `namespace.ErrNotFound` on Issue (#3535).** The port documents the new error and its order; the conformance suite tests it. #3543 and #3547 stay open and are not changed.
- **Adapter label from registration (#3586).** A pattern with two labels stops the start. An unmatched request gets the label `server`, one of the four fixed labels; this is a metric label only, not a decision.
- **Fresh-step code rule (#3620).** The new entry in `docs/reference/code-rules.md:30` documents that the rule has no effective value in the API and no record of the skip. This is the open #3625; no new issue.
- **E2E binary fixes (#3671).** Test harness only: `--trusted-proxies 127.0.0.1/32` on every backend and `PrepareFreshAdmin`.
- **RFC 133.** Draft. Option 1 (ID only) follows ADR 362 decision 1. The RFC is the open question for #3554-like name references in limits and gates; it decides nothing.
- **OpenSpec changes in the range.** Each proposal says "Open questions: none" and lists its ADRs. `first-administrator-one-time-code` quotes ADR 339 and the owner-tagged ADR 385 decision 5 with no conflict.

## Round-15 issues referenced

#3540 and #3545 and #3546 are closed by commits in this range. #3543, #3547, #3548, #3549, #3550, #3551, #3552, #3553, #3554, #3555 and #3556 stay open; no commit in the range changes them. The two new issues do not duplicate them.

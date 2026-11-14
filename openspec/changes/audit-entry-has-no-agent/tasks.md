## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing entry-shape test in the NEW file `pkg/job/agent_audit_test.go`: an agent start entry (`pkg/job/service_start.go:623`) has the agent block and no agent change key. Check: TestAuditAgentBlockHasNoAgentField fails on main, passes after fix against GET /api/audit/entries with an access token.
- [ ] 1.2 Add failing hash test in the NEW file `pkg/job/agent_audit_test.go`: entries with no agent keep the same hash (`pkg/audit/audit.go:408`, func ComputeHash). Check: TestAuditHashStableWithoutAgent fails on main, passes after fix.
- [ ] 1.3 Add failing legacy test in the NEW file `pkg/job/agent_audit_test.go`: an old start entry with the change key reads and verifies, and the block wins when both exist. Check: TestAuditLegacyChangeKeyDualRead fails on main, passes after fix.
- [ ] 1.4 Add failing tamper test in `pkg/audit/audittest/chain.go`: one changed byte of the block refuses the chain. Check: TestAuditTamperedBlockFailsCheck fails on main, passes after fix against GET /api/audit/check with an access token.
- [ ] 1.5 Add failing test in `internal/api/audit/audit.go`'s test file: a missing entry id answers audit.entry-not-found. Check: TestAuditEntryNotFoundCode fails on main, passes after fix against GET /api/audit/entries/{} with an access token.
- [ ] 1.6 Add failing test in `internal/api/audit/audit.go`'s test file: an unreadable entry during the chain check answers audit.entry-read-unavailable. Check: TestAuditEntryReadUnavailableCode fails on main, passes after fix against GET /api/audit/check with an access token.

## 2. Build

- [ ] 2.1 In `pkg/job/service_start.go:623`, remove the agent change key and write the agent block in the same transaction for start entries with an agent ref. Check: Job start with an agent ref, then GET /api/audit/entries with an access token shows the block and no change key.
- [ ] 2.2 Document the agent block of a job entry on `docs/guides/audit-log.md`, with one example; the field of the entry read API comes from the change audit-agent-block. Check: GET /api/audit/entries with an access token returns the block of a job start entry; the docs page shows the example.

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 Run e2e audit read and chain verification over new and legacy entries. Check: GET /api/audit/check with an access token verifies the chain across both shapes.
- [ ] 4.2 Run usability check of the agent-block read path. Check: Persona completes the user path from the docs page alone, and each error names the one command that helps. A person's step.
- [ ] 4.3 Run full wiring, docs page and make check. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).

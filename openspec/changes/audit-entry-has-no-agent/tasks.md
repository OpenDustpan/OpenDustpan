## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing entry-shape test: agent start entry has agent block and no agent change key. Check: TestAuditAgentBlockHasNoAgentField fails on main, passes after fix against GET /api/audit/entries with an access token.
- [ ] 1.2 Add failing hash test: entries with no agent keep the same hash. Check: TestAuditHashStableWithoutAgent fails on main, passes after fix.
- [ ] 1.3 Add failing legacy test: old start entry with change key reads and verifies, block wins when both exist. Check: TestAuditLegacyChangeKeyDualRead fails on main, passes after fix.
- [ ] 1.4 Add failing tamper test: one changed byte of the block refuses the chain. Check: TestAuditTamperedBlockFailsCheck fails on main, passes after fix against GET /api/audit/check with an access token.
- [ ] 1.5 Add failing test: a missing entry id answers audit.entry-not-found. Check: TestAuditEntryNotFoundCode fails on main, passes after fix against GET /api/audit/entries/{} with an access token.
- [ ] 1.6 Add failing test: an unreadable entry during the chain check answers audit.entry-read-unavailable. Check: TestAuditEntryReadUnavailableCode fails on main, passes after fix against GET /api/audit/check with an access token.

## 2. Build

- [ ] 2.1 Remove the agent change key and write the agent block in the same transaction for start entries with an agent ref. Check: Job start with an agent ref, then GET /api/audit/entries with an access token shows the block and no change key.
- [ ] 2.2 Expose the agent block on the entry read API and document its shape. Check: GET /api/audit/entries returns the block; the docs page shows one example.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run e2e audit read and chain verification over new and legacy entries. Check: GET /api/audit/check with an access token verifies the chain across both shapes.
- [ ] 4.2 Run usability check of the agent-block read path. Check: Persona completes the user path from the docs page alone, and each error names the one command that helps.
- [ ] 4.3 Run full wiring, docs page and make check. Check: `make check` with zero findings.

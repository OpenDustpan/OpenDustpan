## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add a failing server test that runs one delegated agent step with body field reason and reads the NEW fields delegation and reason alongside the agent block of audit-entry-has-no-agent from GET /api/audit/entries with an access token. Check: TestAgentStepWritesAuditEntry fails on main, passes after the fix.
- [ ] 1.2 Add a failing server test that denies a delegated call outside the scope through the tool API and checks the denial entry through GET /api/audit/entries with an access token. Check: TestAgentDenialIsAudited fails on main, passes after the fix.
- [ ] 1.3 Add a failing server test that writes delegation entries then calls GET /api/audit/check with an access token and checks the chain verifies and names the first bad sequence on tamper. Check: TestAgentChainVerifies fails on main, passes after the fix.
- [ ] 1.4 Add a failing server test that blocks the database, then calls the existing routes GET /api/audit/entries and GET /api/audit/check with an access token, and checks that both answer 503 with Retry-After 5 and audit.entry-read-unavailable. Check: TestAgentAuditReadsFailFast fails on main, passes after the fix.

## 2. Build

- [ ] 2.1 Write the delegation and reason on each delegated step in the same transaction as an allowed step, and the denial entry in its own transaction for a denied step, with the database sequence per entry. Check: GET /api/audit/entries with an access token returns the NEW delegation and reason fields alongside the agent block of audit-entry-has-no-agent for each delegated step.
- [ ] 2.2 Include the NEW fields and the hash-input version in the hash chain input, keep old entries verifiable with their stored input, and expose the fields on the read paths with their declared actions and objects. Check: GET /api/audit/check with an access token verifies a chain that holds delegation entries and names the first bad sequence number.
- [ ] 2.3 Document the delegation audit fields in the audit log guide. Check: Docs page lists the delegation and reason fields with one copy-paste read command.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add an e2e step on PostgreSQL through the real binary over TLS that checks each agent step is audited and the chain verifies. Check: E2e step delegates, runs one agent step with a reason, lists GET /api/audit/entries with an access token and calls GET /api/audit/check with an access token.
- [ ] 4.2 Run a usability check of the audit read path. Check: Persona completes the path from the docs page alone and each error names the one command that helps.
- [ ] 4.3 Run the full check gate. Check: `make check` with zero findings.

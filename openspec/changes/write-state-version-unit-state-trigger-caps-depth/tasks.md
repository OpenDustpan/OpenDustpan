## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing conformance cases for per-unit and per-source caps on trigger.Store.Put. Check: trigger conformance cap test fails on main: 21st trigger and 51st follower refused with the cap number
- [ ] 1.2 Add failing cases for PUT count rules and lowered caps. Check: trigger conformance PUT test fails on main: same source succeeds at cap, moved source recounts
- [ ] 1.3 Add failing cases for cycle refusal with serialized writes. Check: trigger conformance cycle test fails on main: closing save names the loop, concurrent closing saves serialize
- [ ] 1.4 Add failing tests for the depth counter and the refusal above 10. Check: BLOCKED by write-state-version-unit-state-follows-trigger-start, which adds the trigger start path; then dispatcher depth test with POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}/fire and a namespace administrator access token: depth 11 refused with audit entry and log line, manual fire resets

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire caps, cycle check and depth guard through the trigger routes and the job dispatcher. Check: PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id} with a namespace administrator access token returns 422 with cap number or loop path
- [ ] 3.2 Prove serialization on each adapter. Check: conformance suite passes the concurrent closing-save case on SQLite and PostgreSQL
- [ ] 3.3 Add an e2e step against the real server. Check: make e2e: with a namespace administrator access token, POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers refuses the 21st trigger and PUT refuses a loop save; the depth 10 step is BLOCKED by write-state-version-unit-state-follows-trigger-start
- [ ] 3.4 Write the docs page for caps, cycle errors and the depth guard. Check: make docs-lint passes on the page
- [ ] 3.5 Run the full definition of done with a usability check from the docs page alone. Check: make check reports zero findings

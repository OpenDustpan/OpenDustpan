## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing conformance cases for per-unit and per-source caps on trigger.Store.Put. Check: trigger storetest cap case fails on main via POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers with access token: save 21st trigger fails with cap 20, save 51st follower fails with cap 50
- [ ] 1.2 Add failing conformance cases for PUT count rules and lowered caps. Check: trigger storetest PUT case fails on main via PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id} with access token: same unit and source succeeds at cap, moved source recounts, lowered cap keeps rows
- [ ] 1.3 Add failing conformance cases for save-time cycle refusal with serialized namespace writes. Check: trigger storetest cycle case fails on main via POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers with access token: closing save names loop path, concurrent closing saves serialize
- [ ] 1.4 Add failing tests for the depth counter in the job origin and the refusal above depth 10. Check: dispatcher test fails on main via POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id}/fire with access token: depth 11 refuses with audit entry and log line, missing origin starts at zero, manual fire resets, audit failure keeps refusal

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire caps, cycle check, and depth guard through the trigger routes and the job dispatcher. Check: PUT trigger route returns 422 with cap number and loop path via PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers/{trigger_id} with access token, settings GET shows effective cap values
- [ ] 3.2 Prove per-adapter serialization of trigger writes of one namespace. Check: conformance suite passes the concurrent closing-save case on the in-memory, SQLite, and PostgreSQL adapters
- [ ] 3.3 Add e2e steps against the real server for caps, cycle refusal, and depth stop. Check: e2e against real server via POST /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers with access token: 21st trigger refused, 51st follower refused, loop save refused, chain stops at depth 10 with audit entry
- [ ] 3.4 Write the docs page for trigger caps, cycle errors, and the depth guard. Check: docs page for trigger limits and depth guard passes `make docs-lint`
- [ ] 3.5 Run the full definition of done: wiring, routes with actions, e2e, docs, usability check from the docs page alone. Check: `make check` reports zero findings after wiring routes POST and PUT /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/triggers with access token

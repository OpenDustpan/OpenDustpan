## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. RFC first

- [ ] 1.1 Write the RFC that extends the conformance list of ADR 238 decision 15 and fixes the gate shape of Server.Workspaces, Server.Backups and Server.Restores in NEW file docs/rfc/start-gate-accessors.md. Check: Owner accepts the RFC in NEW file docs/rfc/start-gate-accessors.md that extends the conformance list of ADR 238 decision 15; exercises ANY /api/server/backups with access token.

## 2. Failing tests

- [ ] 2.1 Write failing test TestServerAccessorsRefuseBeforeStart in NEW file pkg/dustpan/start_gate_test.go for calls before Start, after failed Start, after success, and during Start. Check: The NEW test TestServerAccessorsRefuseBeforeStart in NEW file pkg/dustpan/start_gate_test.go fails on main; exercises ANY /api/server/backups with access token through the start gate.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Refine this change when its RFC is accepted. Confirmed by: the driver's gates. Check: BLOCKED by the RFC gate-shape decision; after the RFC, exercises ANY /api/server/backups with access token and lists wiring, docs and usability check.

## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Tests

- [ ] 1.1 Add NEW pkg/gate/schema_guard_test.go with TestNoGateSchemaHasGroupAttribute that loads each gate schema and asserts no Group-typed attribute. Check: go test ./pkg/gate -run TestNoGateSchemaHasGroupAttribute -count=1 passes on current schemas
- [ ] 1.2 Add a permanent table test in NEW pkg/gate/schema_guard_test.go with in-memory schemas for Group, Set of Group, nested record, union, common type, parent chain, and one clean schema. Check: go test ./pkg/gate -run TestSchemaGuardTable -count=1 passes with permanent in-memory schemas
- [ ] 1.3 Assert in NEW pkg/gate/schema_guard_test.go that the loaded schema set equals the explicit expected ID list stored in the test. Check: go test ./pkg/gate -run TestNoGateSchemaHasGroupAttribute -count=1 fails after adding an ID to the expected list test double

## 2. Definition of done

- [ ] 2.1 Run the gate suite for the test-only change. Check: go test -race ./pkg/gate/... passes with zero failures

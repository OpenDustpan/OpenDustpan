## 1. Failing tests first

- [ ] 1.1 Write failing tests for each spec scenario of Pulumi placement and refusal Check: `go test ./internal/api/pulumi -run TestPlacement -count=1` fails on main
- [ ] 1.2 Write failing tests for each spec scenario of TFE project placement and refusal Check: `go test ./internal/api/tfe -run TestWorkspaceProjectPlacement -count=1` fails on main
- [ ] 1.3 Write failing tests for rule validation, hostile patterns, the 100-rule limit, empty rule list, check order, stale rule, rule list order and audit entries Check: `go test ./internal/collectionsvc -run TestPlacementResolver -count=1` fails on main
- [ ] 1.4 Write failing tests for concurrent creates of one name from two Pulumi processes and from Pulumi and TFE, and for a reorder racing a create Check: `go test ./internal/collectionsvc -run TestPlacementConcurrent -count=1` fails on main

## 2. Implementation

- [ ] 2.1 Implement the resolver and wire it into the Pulumi and TFE create paths with the make-unit check Check: `go test ./internal/collectionsvc -run TestPlacementResolver -count=1`
- [ ] 2.2 Write audit entries in the same transaction for placements and rule save, reorder and delete, and the entry of each refusal Check: `go test ./internal/collectionsvc -run TestPlacementAudit -count=1`
- [ ] 2.3 Return the placement rules in match order from the list route Check: `go test ./internal/collectionsvc -run TestPlacementRuleListOrder -count=1`
- [ ] 2.4 Add e2e steps: make the namespace with no catch-all rule (or delete it), make collections with the existing collection create route, then run rule placement, no-match refusal, TFE project placement and unknown project refusal, each with an audit check Check: `make e2e` runs the placement steps with the real CLIs over TLS on SQLite and PostgreSQL

## 3. Definition of done

- [ ] 3.1 Check that the placement rule routes and the collection routes declare actions and pass the route test Check: the route policy test passes for the placement-rules and collections routes
- [ ] 3.2 Update the collections and placement page, with the TFE project steps, in the same pull request Check: each command on the page runs as written in a test
- [ ] 3.3 Do the usability check from the docs page alone and run all checks Check: `make check` with zero findings

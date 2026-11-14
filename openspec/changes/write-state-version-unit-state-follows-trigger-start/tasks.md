## 1. Failing tests first

- [ ] 1.1 Add conformance cases for Trigger When and LastSaver validation Check: pkg/trigger/triggertest dispatcher cases for when applied pass and fail on main before the fix
- [ ] 1.2 Add Upstream idempotency and ErrVersionMismatch conformance cases Check: triggertest Upstream suite shows duplicate-version and older-version cases failing on main
- [ ] 1.3 Add saver read-check conformance cases Check: output read tests with missing, deleted, and disabled saver deny and fail on main

## 2. Implementation

- [ ] 2.1 Implement Dispatcher.Upstream over the final-version store with per-trigger error capture Check: go test ./pkg/trigger/... passes; Upstream verifies the version against the job store and uses one idempotency key per trigger and version
- [ ] 2.2 Wire the OpenTofu final-version writer as the event sender Check: go test ./pkg/... passes; OpenTofu final-version write emits the upstream event, Pulumi emits nothing in this slice
- [ ] 2.3 Publish the declared follower edge to the graph Check: go test ./internal/graph/... passes; follows trigger publishes a declared edge and safe delete names followers
- [ ] 2.4 Carry the trigger ID on the job and check each source output read against the last saver Check: go test ./pkg/outputshare/... plus trigger read tests pass; missing saver denies

## 3. Definition of done

- [ ] 3.1 Wire routes, authorization actions, docs page, and e2e step Check: POST then GET of trigger routes with unit:manage-triggers action passes policy check; make check reports zero findings
- [ ] 3.2 Add e2e step against the real CLI over TLS on SQLite and PostgreSQL Check: e2e with the real OpenTofu CLI applies workspace A and the plan job of workspace B starts once; a repeat event starts nothing
- [ ] 3.3 Write the user docs page in the same pull request Check: docs page names which runs count as an apply for OpenTofu and the Pulumi rule for later; make docs-lint passes
- [ ] 3.4 Run the usability check Check: persona completes the path from the docs page alone; each error names the one command that helps

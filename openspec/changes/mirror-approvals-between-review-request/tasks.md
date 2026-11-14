## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing table tests in NEW file internal/changeordersvc/mirror_test.go for pending review, mirror out, unlinked refusal, revoked link, stale proof, replay, supersede, post failure, request-changes and window 0. Check: `go test ./internal/changeordersvc -run TestMirrorForgeApproveToPending -count=1` fails on main
- [ ] 1.2 Add failing window edge tests in NEW file internal/changeordersvc/freshwindow_test.go for switch on, switch off, narrower rule window and window 0. Check: `go test ./internal/changeordersvc -run TestFreshAuthWindowEdges -count=1` fails on main

## 2. Build

- [ ] 2.1 Implement the mirror service in NEW file internal/changeordersvc/mirror.go with the link check at point of use and one audit entry per direction. Check: `go test ./internal/changeordersvc -count=1` passes
- [ ] 2.2 Add the approve and read routes with authorization actions and the fresh-step refusal shape. Declare the routes in NEW file internal/api/server/changeorderapprovals.go and write the adapter in NEW file internal/api/server/changeorderapprovals_adapter.go. Check: `POST /api/dustpan/v1/change-orders/{id}/approvals` inside and outside the window behaves as specified
- [ ] 2.3 Add the namespace switch and window through the settings resource with audit entries in NEW file internal/changeordersvc/freshwindow.go. Check: `PATCH /api/dustpan/v1/namespaces/{namespace}/settings` sets freshAuthForApprovals and freshAuthSeconds, with an audit entry and a new revision

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add an e2e step for both directions and the read-only view. BLOCKED by git-host-webhook-intake-post: the recorded forge intake route does not exist on main yet Check: e2e step over `POST /api/dustpan/v1/change-orders/{id}/approvals` with an OAuth-linked person credential and a recorded forge passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Write the docs page with the OAuth setup and the refusal text Check: Docs page ships in the same pull request and `make docs-lint` passes
- [ ] 4.3 Run the usability check for the Developer path Check: Developer approves in the forge and in OpenDustpan from the docs page alone. A person's step.
- [ ] 4.4 Run make check with zero findings Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).

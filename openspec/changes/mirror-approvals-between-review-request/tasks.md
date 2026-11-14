## 1. Failing tests

- [ ] 1.1 Add failing table tests for pending review, mirror out, unlinked refusal, revoked link, stale fresh proof, replay idempotency, supersede, forge post failure, request-changes mirror, and window 0 Check: `go test ./internal/changeordersvc -run TestMirrorForgeApproveToPending -count=1` fails on main
- [ ] 1.2 Add failing window edge tests for switch on, switch off, narrower rule window, and window 0 Check: `go test ./internal/changeordersvc -run TestFreshAuthWindowEdges -count=1` fails on main

## 2. Build

- [ ] 2.1 Implement the mirror service with the OAuth link check at point of use and one audit entry per direction Check: `go test ./internal/changeordersvc -count=1` passes
- [ ] 2.2 Add new approve and read routes with authorization actions and the fresh-step refusal shape Check: `POST /api/dustpan/v1/change-orders/{id}/approvals` inside and outside the window behaves as specified
- [ ] 2.3 Add the namespace switch and window through the settings resource with audit entries Check: `PATCH /api/dustpan/v1/namespaces/{namespace}/settings` sets the approvals switch and window with action namespace:manage-settings

## 3. Definition of done

- [ ] 3.1 Add e2e step with the recorded forge for both directions and the read-only view Check: Recorded forge run shows one approve each way and the read-only refusal without OAuth
- [ ] 3.2 Write the docs page with the OAuth setup and the refusal text Check: Docs page for mirrored approvals and the namespace switch ships in the same pull request
- [ ] 3.3 Run the usability check for the Developer path Check: Developer approves in the forge and in OpenDustpan from the docs page alone
- [ ] 3.4 Run make check with zero findings Check: `make check` reports zero findings

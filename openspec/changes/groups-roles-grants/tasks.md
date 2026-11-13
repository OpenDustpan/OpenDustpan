## 1. Failing tests

- [ ] 1.1 Add failing e2e for writer scope across two collections Check: e2e writer on collection A writes unit of A and gets 403 on unit of B
- [ ] 1.2 Add failing e2e for viewer read and write Check: e2e viewer reads export and pull and gets 403 on up and apply, in both collections
- [ ] 1.3 Add failing e2e for revocation and membership timing Check: e2e revoke grant and end membership then next CLI request fails with 403
- [ ] 1.4 Add failing e2e for access-review snapshot and history export Check: e2e export lists holder, role, object, creator, time, last use with Never, and history end time
- [ ] 1.5 Add failing e2e for non-admin baseline Check: e2e non-admin token runs milestone 1 to 3 Pulumi and OpenTofu reads and writes with collection grants

## 2. Implementation

- [ ] 2.1 Enforce collection-scoped grant checks on unit reads and writes with installation-grant reach Check: `go test ./internal/access/... -run TestCollectionScope`
- [ ] 2.2 Refuse a grant above the granter reach through the shared reach function Check: `go test ./internal/access/... -run TestGranterReach`
- [ ] 2.3 Serve snapshot and history export with the required row fields, action and audit entry Check: `go test ./internal/access/... -run TestAccessReviewExport`

## 3. Definition of done

- [ ] 3.1 Wire routes with authorization actions and audit entries Check: e2e roadmap step writer on one collection writes there and gets 403 elsewhere, viewer reads but refuses up and apply, revocation applies on next request, export lists each grant
- [ ] 3.2 Document groups, roles, grants and the export in the same pull request Check: docs page command runs as written
- [ ] 3.3 Run usability check with error names for the one helping command Check: persona completes the path from the docs page alone
- [ ] 3.4 Run full definition of done Check: `make check` with zero findings

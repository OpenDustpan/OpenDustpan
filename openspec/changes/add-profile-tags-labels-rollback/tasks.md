## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Build

- [ ] 1.1 Add failing profiletest cases for SetTag, Retract, At with tag, rollback, labels and diff first in pkg/profile/profiletest/cases.go:1. Check: profiletest in pkg/profile/profiletest/cases.go:1 fails on main: retract moves tags, retract of newest fails, routing tag move and rollback need profile:open, diff with showSecrets needs profile:open, tag and label limits, latest fixed, fence and audit entries
- [ ] 1.2 Implement store methods, fences, backup rows and portable spec entries in pkg/profile/store.go and pkg/store/sqlite/profile.go. Check: go test ./pkg/profile/... ./pkg/store/... with sqlite and postgres suites green
- [ ] 1.3 Wire the routes of the ADR 203 decision 12 table for tags, labels, rollback, retract and diff in internal/api/profile/routes.go:26 with notifications profile.tag-changed, profile.label-changed and profile.revision-retracted. Check: Route test in internal/api/profile/routes.go:26 passes including %2F profile and tag names, label list with labelKey and labelValue, retract and rollback routes, Idempotency-Key replay, rename by prf- ID keeping tags, audit content and showSecrets refusal

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Add e2e profiles step for tag, rollback and diff against GET /api/dustpan/v1/namespaces/{}/profiles with an access token. Check: e2e profiles step calls GET/POST /api/dustpan/v1/namespaces/{}/profiles with an access token to tag, roll back and diff against the real server
- [ ] 3.2 Update the profiles API docs page in docs/reference/profiles-api.md. Check: docs/reference/profiles-api.md updated in the same pull request
- [ ] 3.3 Run usability check from the docs page and make check with zero findings. Check: `make check` reports zero findings

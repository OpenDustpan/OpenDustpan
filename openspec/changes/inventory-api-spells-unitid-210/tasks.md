## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for dual unitId and unitID on listUnits and get-by-ID in internal/api/inventory/inventory.go:432. Check: NEW test TestDualUnitId in internal/api/inventory fails on main, passes after fix.
- [ ] 1.2 Add failing test for dual unitId on DeletedUnit list in internal/api/inventory. Check: NEW test TestDualDeletedUnitId in internal/api/inventory fails on main, passes after fix.
- [ ] 1.3 Add failing test for dual versionId and versionID on getJob closed answer and absence on open answer in internal/api/jobs/serve.go:177. Check: NEW test TestDualVersionId in internal/api/jobs fails on main, passes after fix.
- [ ] 1.4 Add failing test for dual unitId and unitID on ContextObject in internal/api/objectcontext/operations.go:70. Check: NEW test TestDualContextUnitId in internal/api/objectcontext fails on main, passes after fix.
- [ ] 1.5 Add failing test for dual tokenId, sourceIp, requestId, objectId and jobId in audit answers in internal/api/audit/audit.go:137. Check: NEW test TestDualAuditFields in internal/api/audit fails on main, passes after fix.
- [ ] 1.6 Add failing test in the NEW file internal/api/inventory/conflict_test.go for 400 on a request with different values in both spellings of one renamed field. Check: NEW test TestRefuseConflictingSpellings in internal/api/inventory/conflict_test.go fails on main, passes after fix with NEW error code request.field-conflict.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire dual fields in Unit, DeletedUnit and ContextObject answers in internal/api/inventory/inventory.go:432 and internal/api/objectcontext/operations.go:70. Check: GET /api/dustpan/v1/namespaces/{namespace}/units with access token returns unitId equal to unitID.
- [ ] 3.2 Wire dual versionId in the closed getJob answer in internal/api/jobs/serve.go:177. Check: GET job route with access token on closed job returns versionId equal to versionID.
- [ ] 3.3 Update the API description in internal/api/apidesc/body.go:252 to list new names and keep old names on LegacyFieldNames as deprecated. Check: TestReflectorAgreesWithTheEncoder in internal/api/apidesc/schema_test.go:309 passes with new names current and old names deprecated.
- [ ] 3.4 Wire dual acronym and URL fields in audit (internal/api/audit/audit.go:137), installation, runner, agent-result, history and job store answers in the same pass. Check: Reads with an access token return equal values in both spellings.
- [ ] 3.5 Move server readers to unitId and versionId, then remove old spellings and LegacyFieldNames entries before stable. Check: GET /api/dustpan/v1/namespaces/-/units with access token returns unitId on each item, and removal task stays open until server readers move.
- [ ] 3.6 Run e2e list of units on GET /api/dustpan/v1/namespaces/{namespace}/units with an access token against the real CLI, update the docs page for the renamed fields, and run the usability check from the docs page alone. Check: GET /api/dustpan/v1/namespaces/{namespace}/units with access token returns unitId on each item, plus `make check` with zero findings.

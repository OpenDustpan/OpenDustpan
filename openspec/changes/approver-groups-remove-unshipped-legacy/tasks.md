## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing store and decode tests for unpaired group IDs in NEW pkg/gate/approvers_ids_test.go Check: NEW pkg/gate/approvers_ids_test.go TestStoredApproversRefuseMissingIDs fails on main, passes after
- [ ] 1.2 Add failing read and job-start tests for deleted and recreated groups in NEW pkg/gate/approvers_deleted_test.go Check: NEW pkg/gate/approvers_deleted_test.go TestApproverReadMarksDeletedGroup fails on main, passes after
- [ ] 1.3 Add failing name-lookup, rename display, and transient-error tests for reads in NEW pkg/gate/approvers_names_test.go Check: NEW pkg/gate/approvers_names_test.go TestApproverReadUsesNameLookup fails on main, passes after

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Require paired IDs on stored shapes and delete legacy branches Check: `go test -race ./pkg/gate/...` passes
- [ ] 3.2 Mark missing groups deleted in reads by ID and refuse them at job start, keep PUT writable for rewrites Check: `go test -race ./pkg/gate/...` passes
- [ ] 3.3 Add name-only directory lookup for reads, keep counting for saves, fail other directory errors closed, recheck at job start Check: `go test -race ./pkg/gate/...` passes
- [ ] 3.4 Wire the gate read and write to the paired-ID check and name lookup on PUT /api/dustpan/v1/namespaces/{namespace}/gates/{id} with an access token Check: PUT /api/dustpan/v1/namespaces/{namespace}/gates/{id} with an access token passes; GET /api/dustpan/v1/namespaces/{namespace}/gates/{id} shows names and deleted marks. Confirmed by: server test with an access token.
- [ ] 3.5 Update the API description and user docs for the deleted mark and the rewrite path Check: API description diff shows the NEW boolean field deleted on approver groups
- [ ] 3.6 Run docs page check and usability check of the rewrite path. A person's step. Check: Persona completes the rewrite path from the docs page alone. Confirmed by: usability check.
- [ ] 3.7 Run full checks. Confirmed by: CI (`make check` with zero findings). Check: `make check` passes with zero findings

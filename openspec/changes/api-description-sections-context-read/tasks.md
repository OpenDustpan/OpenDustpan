## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing test of the eight named section schemas in the NEW file pkg/dustpan/apidesc_context_sections_test.go against GET /api/dustpan/v1/openapi.json. Check: `go test ./pkg/dustpan -run TestPerSectionContextSchemas -count=1` fails on main
- [ ] 1.2 Write the failing validation test with an inline subset validator in the NEW file pkg/dustpan/apidesc_context_answers_test.go. It reads real answers of GET getUnitContext and GET getNamespaceContext with an access token, including empty arrays and omitted pointer members. Check: `go test ./pkg/dustpan -run TestContextAnswersMatchDescription -count=1` fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Fix and definition of done

- [ ] 3.1 Add the eight named section schemas to the API description and normalize nil slices in the NEW unexported answer constructor in pkg/objectcontext. Check: `go test ./pkg/dustpan ./pkg/objectcontext -count=1` passes
- [ ] 3.2 Verify GET getUnitContext and GET getNamespaceContext with an access token and GET getApiDescription, update the context read docs page for the per-section shapes, and run make check with zero findings. A person's step. Check: `GET /api/dustpan/v1/openapi.json` lists the eight named schemas; `make check` passes with zero findings

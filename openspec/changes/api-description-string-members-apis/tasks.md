## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Convert fixed sets

- [ ] 1.1 Write failing open-enum tests for inventory, jobs, sink, and preferences sets in internal/api/apidesc/schema_test.go. Check: `go test ./internal/api/apidesc/ -run TestReflectorAgreesWithTheEncoder -count=1`
- [ ] 1.2 Convert each member on the closed convert list to a named Go type with constants and DeclareOpenEnum route-label entries, then regenerate internal/api/apidesc/openapi.json. Confirmed by: CI (schema checks). Check: `go test ./pkg/dustpan/ -run TestDescriptionSchemasAreConsistent -count=1`

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Check the wired description read and update the docs page with the closed convert list and the stays-string rule. Check: `curl $SERVER/api/dustpan/v1/openapi.json` shows open enums for converted members and plain string for other members
- [ ] 3.2 Rebuild affected generated clients and run their build checks for the Embedder path. Check: Generated Go and TypeScript clients build with unchanged wire values
- [ ] 3.3 Run the definition of done: description tests, wired description read, docs page and generated-client usability check. Confirmed by: CI (make check). Check: `make check` with zero findings

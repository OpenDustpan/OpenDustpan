## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. tests

- [ ] 1.1 In NEW pkg/dustpan/apidesc_examples_test.go add failing test NEW TestDescriptionExamplesDecode that every example validates against its schema Check: NEW TestDescriptionExamplesDecode fails on main, passes after fix
- [ ] 1.2 In NEW pkg/dustpan/apidesc_examples_test.go add hostile test NEW TestDescriptionExamplesHoldNoSecrets that scans nested objects and arrays for denied secret patterns Check: NEW TestDescriptionExamplesHoldNoSecrets rejects secret-like fixtures

## 2. implement

- [ ] 2.1 In NEW pkg/dustpan/apidesc_examples.go build NEW exampleRegistry through NEW newExampleRegistry, and serve the examples on the existing route GET /api/dustpan/v1/openapi.json through its adapter internal/api/server/openapi.go Check: The answer of GET /api/dustpan/v1/openapi.json holds one example per JSON body and none for 204 answers

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. definition of done

- [ ] 4.1 Measure description growth against the quality budget Check: Size check on GET /api/dustpan/v1/openapi.json: difference with and without examples stays below 500 KB
- [ ] 4.2 Run usability read of examples from the description text alone Check: Embedder copies one request example from the description and decodes it with the generated client
- [ ] 4.3 Run full checks Check: `make check` passes with zero findings

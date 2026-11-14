## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 In e2e/e2e_test.go:116 extend TestCLIs with a failing test for an unlisted read that calls GET /.well-known/terraform.json with no credential; BLOCKED by missing Pulumi routes for the 4 reads. Check: TestCLIs fails on unlisted read in e2e/e2e_test.go:116. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.2 In e2e/e2e_test.go:116 extend TestCLIs with a passing test for a refused entry that calls GET /.well-known/terraform.json with no credential; BLOCKED by missing Pulumi routes for the 4 reads. Check: TestCLIs passes on refused read in e2e/e2e_test.go:116. Confirmed by: CI (the agent writes the e2e test).

## 2. Contract evidence

- [ ] 2.1 In e2e/e2e_test.go:116 capture the 4 Pulumi 3.273.0 reads that answer 403 from a TestCLIs run with an access token; BLOCKED by missing Pulumi routes for the 4 reads. Check: Report lines list 4 method and path pairs from e2e/e2e_test.go:116. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.2 Write NEW file e2e/testdata/pulumi-3.273.0.contract.json with each of the 4 reads as served or refused with a reason, checked by `make e2e` with an access token; BLOCKED by missing Pulumi routes for the 4 reads. A person's step. Check: `make e2e` passes with Pulumi CLI 3.273.0
- [ ] 2.3 Write NEW file e2e/testdata/opentofu-1.13.2.contract.json that records the modules.v1 discovery answer checked by `make e2e` with no credential, using code in internal/api/tfe/tfe.go:492; BLOCKED by the modules.v1 announce decision. A person's step. Check: `make e2e` passes with OpenTofu 1.13.2

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 In e2e/e2e_test.go:116 rerun the older-version contract files with an access token for Pulumi reads and with no credential for discovery; BLOCKED by missing Pulumi routes for the 4 reads. A person's step. Check: `make e2e` passes for every contract file
- [ ] 4.2 In e2e/e2e_test.go:116 wire a served route with its authorization actions only when a read is classified as serve with an access token; BLOCKED by missing Pulumi routes for the 4 reads. Check: Route test passes for a served read. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 In e2e/e2e_test.go:116 check usability so each failure names the read and `make e2e`; BLOCKED by missing Pulumi routes for the 4 reads. A person's step. Check: `make check` reports zero findings

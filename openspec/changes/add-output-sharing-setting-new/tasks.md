## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for default nobody deny, collection rule allow, rule removal deny and invalid target. Check: TestOutputSharingDefaultNobodyDenies fails on main
- [ ] 1.2 Write failing e2e steps for the Pulumi stack reference and OpenTofu remote state read. Check: e2e output sharing steps fail on main

## 2. Build

- [ ] 2.1 Add the namespace setting with its effective value. Check: PATCH then GET /api/dustpan/v1/namespaces/{namespace}/settings shows the value
- [ ] 2.2 Add namespace and collection rules to the existing output-sharing route. Check: PUT then GET /api/dustpan/v1/namespaces/{namespace}/output-sharing shows the rule by ID
- [ ] 2.3 Wire the read check and the audit entries after #897 is merged. Check: TestOutputReadChecksRuleAndGrant passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run the e2e steps against the real CLIs on SQLite and PostgreSQL. Check: make e2e
- [ ] 4.2 Add docs/guides/output-sharing.md. Check: make docs-lint
- [ ] 4.3 Run the usability check from the docs page alone. Check: Each error names one command
- [ ] 4.4 Run the full check. Check: make check with zero findings

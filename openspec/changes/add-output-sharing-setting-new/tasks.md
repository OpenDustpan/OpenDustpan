## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for default nobody deny, collection rule allow, rule removal deny and invalid target, in the existing file pkg/outputshare/outputshare_test.go (extend it in place). Check: TestOutputSharingDefaultNobodyDenies fails on main
- [ ] 1.2 Write failing e2e steps for the Pulumi stack reference read and the OpenTofu remote state read, in the existing file e2e/output_sharing_test.go (extend it in place) that uses e2e/tool.go:110 (func OpenTofu). BLOCKED by #897 for the job token path. Check: e2e output sharing steps fail on main. Confirmed by: CI (the agent writes the e2e test).

## 2. Build

- [ ] 2.1 Add the namespace setting with its effective value in pkg/setting. Declare the setting in internal/api/apidesc and serve it in the NEW file internal/api/server/output_sharing_setting.go on the existing namespace settings route (the catalog at internal/errcode/catalog_settings.go:7 stays). Check: PATCH then GET /api/dustpan/v1/namespaces/{namespace}/settings shows the value
- [ ] 2.2 Add namespace and collection rules to the existing output-sharing route: the domain in the NEW file pkg/outputshare/rule.go, the declaration in internal/api/apidesc, the handler in internal/api/server. Check: PUT then GET /api/dustpan/v1/namespaces/{namespace}/output-sharing shows the rule by ID
- [ ] 2.3 Wire the read check and the audit entries in the NEW file internal/outputsharesvc/check.go after #897 is merged. Check: TestOutputReadChecksRuleAndGrant passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run the e2e steps in e2e/output_sharing_test.go against the real Pulumi CLI and OpenTofu on SQLite and PostgreSQL. BLOCKED by #897 for the job token path. Check: make e2e. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Add docs/guides/output-sharing.md in the same pull request. Check: make docs-lint
- [ ] 4.3 Run the usability check from docs/guides/output-sharing.md alone. Check: Each error names one command. A person's step.
- [ ] 4.4 Run the full check over the whole repository. Check: make check with zero findings. Confirmed by: CI (the agent runs the fast checks only).

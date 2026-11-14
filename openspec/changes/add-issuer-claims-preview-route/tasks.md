## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write NEW internal/api/issuer/claims_preview_test.go with failing handler test for claims and subject. Check: TestPreviewClaimsAnswersSubject fails on main.
- [ ] 1.2 Write NEW internal/api/issuer/claims_preview_test.go with failing test that preview calls no signer and no adapter. Check: TestPreviewSignsNothing fails on main.
- [ ] 1.3 Write NEW internal/api/issuer/claims_preview_test.go with failing refusal test for caller without profile:read. Check: TestPreviewNeedsProfileRead fails on main.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Edit internal/api/issuer/routes.go to add NEW POST .../issuer/claims-preview with profile:read, called with an access token. Check: POST .../issuer/claims-preview answers claims and subject.
- [ ] 3.2 Edit internal/api/route to declare the authorization action and pass the route and policy tests. Check: Route test in internal/api/route passes.
- [ ] 3.3 Edit NEW internal/api/issuer/claims_preview_test.go to add parity test of preview and signed claims. Check: Table test in NEW internal/api/issuer/claims_preview_test.go passes.
- [ ] 3.4 Edit docs/reference/issuer-api.md to document the NEW route and regenerate the API description. Confirmed by: CI (`make docs-lint`). Check: make docs-lint passes.
- [ ] 3.5 Run usability check for preview from docs/reference/issuer-api.md. A person's step. Check: Persona completes preview from docs page alone.
- [ ] 3.6 Run make check for the preview change. Confirmed by: CI (`make check`). Check: `make check` reports zero findings.

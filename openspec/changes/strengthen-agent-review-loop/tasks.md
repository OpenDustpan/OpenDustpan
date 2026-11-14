## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Guide text

- [ ] 1.1 Add the fixed security checklist for diffs that touch secrets, identity, routes, errors, migrations, limits, locks or crypto Check: No route: the test reads the NEW file docs/guides/agent-review-loop.md only.
- [ ] 1.2 Add the audit cadence, agent-drafted RFC marking, triage labels and branch hygiene Check: No route: the test reads the NEW file docs/guides/agent-review-loop.md only.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Walk the guide from the page alone and record where the path costs time Check: No route: the walk reads docs/guides/agent-review-loop.md only.
- [ ] 3.2 Run make check with zero findings. Confirmed by: CI (make check). Check: make check

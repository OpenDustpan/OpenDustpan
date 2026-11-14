## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Setup

- [ ] 1.1 Set up the hosted announce-only list on opendustpan.org with double opt-in and tracking off after the owner picks the service and record the address in NEW docs/guides/announce-list.md. A person's step. Check: No route: open the hosted sign-up page and confirm SPF, DKIM, DMARC pass with tracking off.
- [ ] 1.2 Add the sign-up link to NEW docs/guides/announce-list.md. A person's step. Check: No route: the link in NEW docs/guides/announce-list.md opens the live sign-up page with no third-party scripts.
- [ ] 1.3 Add the post-to-list step for each release and security notice to NEW docs/guides/release-checklist.md. A person's step. Check: No route: read NEW docs/guides/release-checklist.md and confirm it names the list, the poster, and the verify step.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Confirm subscribe, plain message, and unsubscribe with a test address against NEW docs/guides/announce-list.md. A person's step. Check: No route: subscribe, confirm, read a plain notice, and unsubscribe with a test address.
- [ ] 3.2 Run docs checks and the usability walk from the docs page alone for NEW docs/guides/announce-list.md. Confirmed by: CI (`make docs-lint`). Check: `make docs-lint` reports zero findings.

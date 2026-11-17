## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Owner answers and failing checks first

- [x] 1.1 Lifted. ADR 379 decisions 1-3 answer questions 1-3 (docs/adr/0379-the-openpgp-dependency-of-the-registry.md). Check: the file exists on main.
- [x] 1.2 Show the current module versions from go.mod (file go.mod on main). Check: `go list -m github.com/ProtonMail/go-crypto github.com/cloudflare/circl golang.org/x/crypto` shows the versions the ADR will record.
- [x] 1.3 Show the frozen-package import proof and the fresh vulnerability output. Compare both with docs/adr/0379-the-openpgp-dependency-of-the-registry.md. Check: `rg -n -F --glob '!*_test.go' 'golang.org/x/crypto/openpgp' pkg internal cmd` and `make vuln` output saved for the ADR.

## 2. Record and check

- [x] 2.1 Verify docs/adr/0379-the-openpgp-dependency-of-the-registry.md. Check: The ADR cites the owner decisions, the versions, the risk owner, the review date 2027-02-14 and the trigger.
- [x] 2.2 Check the versions in docs/adr/0379-the-openpgp-dependency-of-the-registry.md against go.mod. Check: `go list -m` output matches the ADR, including indirect x/crypto.
- [x] 2.3 Verify fresh vulnerability output in docs/adr/0379-the-openpgp-dependency-of-the-registry.md. Check: `make vuln` output with affected symbol count matches the ADR; on a differing result, cite ADR 379 decision 2.
- [ ] 2.4 Move golang.org/x/crypto in go.mod from the pseudo-version to the tag v0.59.0 on or after 25 November 2026, with the owner's word (ADR 379 decision 3) and a clean make vuln. Check: `go list -m golang.org/x/crypto` shows v0.59.0. A person's step.

## 3. Side effects (computed from main)

- [x] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [x] 4.1 Add one plain statement to the signature section of the existing guide docs/guides/private-registry.md: the registry checks signatures with the OpenPGP packages of the go-crypto module, and `go version -m` on the binary shows the version. Do not cite an ADR, an RFC or an issue on the page. Check: `make docs-lint` and `make check` report zero findings, and `rg -n -i -E '\b(ADR|RFC) ?[0-9]+|issues/[0-9]+' docs/guides/private-registry.md` finds no hit; the Operator finds the library and the command from the docs page alone. A person's step. Confirmed by: CI (the agent runs the fast checks only).

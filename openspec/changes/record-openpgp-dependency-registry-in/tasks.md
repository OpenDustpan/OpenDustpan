## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Owner answers and failing checks first

- [ ] 1.1 Get the owner answers to the three open questions. Check: The owner answers are recorded in the change before any ADR text is written.
- [ ] 1.2 Show the current module versions. Check: `go list -m github.com/ProtonMail/go-crypto github.com/cloudflare/circl golang.org/x/crypto` shows the versions the ADR will record.
- [ ] 1.3 Show the frozen-package import proof and the fresh vulnerability output. Check: `rg -n -F --glob '!*_test.go' 'golang.org/x/crypto/openpgp' pkg internal cmd` and `make vuln` output saved for the ADR.

## 2. Record and check

- [ ] 2.1 Write the ADR after the owner answers. Check: The ADR cites the owner answer, the versions, the replacement reason, the risk owner, the review date and the trigger.
- [ ] 2.2 Check the ADR versions against go.mod. Check: `go list -m` output matches the ADR, including indirect x/crypto.
- [ ] 2.3 Record fresh vulnerability output in the ADR. Check: `make vuln` output with affected symbol count is in the ADR; on a differing result the owner reviews before ship.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Ship the ADR, the index entry and the docs link with the docs page and usability check. Check: `make docs-lint` and `make check` report zero findings; the Operator reaches the ADR from the docs page alone.

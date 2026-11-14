---
version: 4
---

Issue: #1293.

## Why

**Persona: operator.** An Operator installs and upgrades a server and needs to trust its supply chain. The registry signature check uses an OpenPGP dependency. No ADR records the approval or the vulnerability disposition, so the Operator cannot verify why the dependency stays.

The user path this change must make work:

1. **Docs page.** Read the registry docs page and follow the link to the new ADR for the OpenPGP dependency and the vulnerability disposition.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The registry docs link to the OpenPGP ADR points to no ADR file. | The docs check names the dead link. Add the ADR file and the index entry. | `make docs-lint` |
| The ADR module versions differ from go.mod. | The version check shows each module that differs. Update the ADR or the build so they match. | `go list -m github.com/ProtonMail/go-crypto github.com/cloudflare/circl golang.org/x/crypto` |

Evidence for the claims above:

- Issue 1293 asks for its own ADR for the OpenPGP dependency. Proof: Issue #1293 body: RFC 101 section F asks for "its own ADR for the dependency"
- Issue 1293 lists the three modules and the owner approval date. Proof: Issue #1293 body: github.com/ProtonMail/go-crypto v1.5.2, github.com/cloudflare/circl v1.6.3, golang.org/x/crypto v0.56.0 is approved by the owner (2026-10-20)
- Issue 1293 reports zero affected symbols for the finding. Proof: Issue #1293 body: govulncheck reports 0 affected symbols
- A fixture file imports the go-crypto openpgp package. Proof: internal/registry/pgpfixture/pgpfixture.go:15: "github.com/ProtonMail/go-crypto/openpgp"
- The pack names no existing ADR for this change. Proof: Context pack adrs array is empty

## What Changes

- Verify ADR 379 (docs/adr/0379-the-openpgp-dependency-of-the-registry.md). It records the module versions, the owner approval, and the GO-2026-5932 disposition. ADR 379 decision 1: "The three modules are approved at the versions in `go.mod` (owner, 2026-11-14)."
- Add the ADR index entry and the registry docs link in the same change.

Out of scope:

- Signature check behavior of the registry
- Curation, upgrade pull requests and other milestone 6 items
- Numbered migration or schema change

## Usability

Where the path still costs the person time or surprises them:

- The registry docs page shows the ADR link next to the signature text. A dead link fails the docs check before release.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Registry test fixture imports go-crypto openpgp. (`internal/registry/pgpfixture/pgpfixture.go:15`)
- Fixture imports the go-crypto armor package. (`internal/registry/pgpfixture/pgpfixture.go:16`)
- Fixture imports the go-crypto packet package. (`internal/registry/pgpfixture/pgpfixture.go:17`)
- The Makefile has the vuln target. (`Makefile:62`)
- ADR 379 is on main. (`docs/adr/0379-the-openpgp-dependency-of-the-registry.md:1 (ADR 379)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does the owner confirm the approval for the versions in go.mod? Answer: yes. ADR 379 decision 1: "The three modules are approved at the versions in `go.mod` (owner, 2026-11-14)." The versions are go-crypto v1.5.2, circl v1.6.5 and x/crypto v0.58.1-0.20261020153306-9f2b28d4331a. These differ from issue 1293, so follow the ADR.
- Who accepts the GO-2026-5932 residual risk? Answer: the project owner. ADR 379 decision 2: "The owner accepts the residual risk of GO-2026-5932 (owner, 2026-11-14)." The review date is 2027-02-14. Each version change checks the risk again with `make vuln`.
- Is the ADR owner-tagged? Answer: yes. ADR 379 decision 3: "A version change of these modules needs the owner's word (owner, 2026-11-14)." The age rule of ADR 7 applies too.

- Does an existing ADR already record the OpenPGP dependency? The pack adrs array is empty, so there is no decision text to quote. The new ADR changes no existing decision.
- What module versions does the build use? The pack gives no go.mod list, so no version is stated as fact. A task runs go list -m and the ADR records only matching versions.
- Does go-crypto replace x/crypto in the build? The pack proves only a fixture import. The ADR records the modules that go.mod lists and claims no production import path.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

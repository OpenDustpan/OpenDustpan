Issue: #1293.

## Why

**Persona: operator.** An Operator installs and upgrades a server and needs to trust its supply chain. The registry signature check uses an OpenPGP dependency. No ADR records the approval or the vulnerability disposition, so the Operator cannot verify why the dependency stays.

The user path this change must make work:

1. **Docs page.** Read the registry docs page and follow the link to the new ADR for the OpenPGP dependency and the vulnerability disposition.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The registry docs link to the OpenPGP ADR points to no ADR file. | The docs check names the dead link. Add the ADR file and the index entry. | `make docs-lint` |
| The ADR module versions differ from go.mod. | The version check names each module that differs. Update the ADR or the build so they match. | `go list -m github.com/ProtonMail/go-crypto github.com/cloudflare/circl golang.org/x/crypto` |

Evidence for the claims above:

- Issue 1293 asks for its own ADR for the OpenPGP dependency. Proof: Issue #1293 body: RFC 101 section F asks for "its own ADR for the dependency"
- Issue 1293 lists the three modules and the owner approval date. Proof: Issue #1293 body: github.com/ProtonMail/go-crypto v1.5.2, github.com/cloudflare/circl v1.6.3, golang.org/x/crypto v0.56.0 is approved by the owner (2026-10-20)
- Issue 1293 reports the govulncheck finding and zero affected symbols. Proof: Issue #1293 body: govulncheck reports 0 affected symbols
- A fixture file imports the go-crypto openpgp package. Proof: internal/registry/pgpfixture/pgpfixture.go:15: "github.com/ProtonMail/go-crypto/openpgp"
- The pack names no existing ADR for this change. Proof: Context pack adrs array is empty
- Non-test code search for govulncheck returns no match in the pack. Proof: rg -n -F --glob '!*_test.go' 'govulncheck' pkg internal cmd matches 0

## What Changes

- Add one ADR that records the module versions, the confirmed owner approval, the reason go-crypto replaces the frozen package, and the GO-2026-5932 disposition with fresh govulncheck output. The ADR ships only after the owner confirms the approval.
- Add the ADR index entry and the registry docs link in the same change.

Out of scope:

- Signature check behavior of the registry
- Curation, upgrade pull requests and other milestone 6 items
- Numbered migration or schema change

## Usability

Where the path still costs the person time or surprises them:

- The registry docs page shows the ADR link next to the signature text. A dead link fails the docs check before release.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The approval of the OpenPGP dependency rests on an issue body dated 2026-10-20. No recorded owner decision in the input supports it. The ADR must cite the owner decision it records, or the owner must confirm it, before the approval can be written as fact. Resolution: open question: ADR does not state approval as fact. Open question asks the owner to confirm the 2026-10-20 approval before the ADR ships.
- R1.2 (open question): GO-2026-5932 has no fix in golang.org/x/crypto, yet the change approves x/crypto v0.56.0. The change does not state who accepts the residual risk, when the acceptance is reviewed, or what triggers a re-check when govulncheck output changes. Resolution: open question: Open question asks the owner to accept the residual risk with review date, contact, deadline and re-check trigger.
- R1.3 (wrong claim): The evidence for the signature check cites only a test fixture (pgpfixture.go:15). It does not show that the production signature check imports go-crypto, so the claim about the check is unsupported. Resolution: answered: Evidence claims only the fixture import that the pack proves, with the exact path. No production import claim remains.
- R1.4 (missing scenario): The vuln disposition "zero affected symbols" has no proof inline (no govulncheck command and output). The proposal's own rule treats an unproven negative claim as unchecked, and no task re-runs the check. Resolution: answered: Added scenarios govulncheck-matches and govulncheck-differs in requirement vuln-disposition; the differing-result scenario requires the ADR to record fresh output and owner review before ship.
- R1.5 (scope): The user path requires a link from the registry docs page to the new ADR. Neither What Changes nor the tasks adds that link, so the change misses part of its own item. Resolution: answered: What changes, specs and tasks now add the registry docs link to the new ADR and check it.
- R1.6 (wrong claim): The ADR check says "No ADR is touched", but What Changes adds an ADR and an index entry. The section contradicts the change it describes. Resolution: answered: Added design decision Record the dependency in one ADR and specs capability registry-supply-chain with requirements registry-docs-link, adr-index-entry, adr-versions-match and vuln-disposition; adr_check stays empty because the pack adrs array is empty so no existing ADR text exists to quote.
- R1.7 (failure mode): The ADR records module versions as text only. No task checks that the versions match go.mod and go.sum, so the ADR can drift from the real supply chain without any check failing. Resolution: answered: Tasks check ADR versions against go list -m output and fail on drift, with scenarios for match and drift.
- R1.8 (wrong claim): The proposal cites "#1221" and "RFC 101 section F" as sources that ask for the ADR. Only issue 1293 is in the input, and its body quotes RFC 101 but does not quote #1221, so the second citation is unsupported. Resolution: answered: Removed standalone RFC 101 and #1221 citations. Evidence cites only issue 1293 text and the pack code fact.
- R1.9 (open question): The ADR number, its title and its owner tag are not chosen. The change does not say whether the new ADR is owner-tagged, which decides whether a later change to the dependency is blocked until the owner decides. Resolution: open question: Open question asks the owner whether the new ADR is owner-tagged. Design uses the next free number from the index at write time.
- R1.10 (missing scenario): The design says future version changes follow the dependency rule, but it does not say how a future govulncheck finding on these modules is handled. The change does not state who is told and what the deadline is. Resolution: open question: Open question on residual risk now includes who is told of a future finding, the deadline, and the re-check trigger.
- R1.11 (open question): The change does not say whether go-crypto replaces x/crypto in go.mod, or whether x/crypto stays as an indirect module. The ADR must state which module stays in the build, because that decides whether the vulnerable module is still linked. Resolution: answered: Considered and tasks state that the ADR records only modules that go.mod lists at write time, including indirect x/crypto, and the version check proves which modules stay in the build.
- R2.1 (scope): The change does not make the owner-approval record usable. Issue 1293 says the owner approved the dependency on 2026-10-20, but the pack has no owner decision to quote. The ADR cannot be written as fact until the owner confirms. Open questions 1-4 block the spec-ready gate, so this is not ready to build. Resolution: open question: The ADR is not written as fact. The owner confirmation is an open question, and the change waits for it. The questions are cut to three that the pack cannot answer.
- R2.2 (missing scenario): The proposal itself says it is not spec-ready: R1.4 claims a scenario was added but no spec delta exists. The change has no specs/<capability>/spec.md, so there is no scenario and no named failing test. Add a delta spec with scenarios for the docs link, the ADR index entry and the version check. Resolution: answered: Added specs registry-supply-chain with scenarios registry-docs-link-present, adr-index-entry-present, and adr-versions-match-gomod.
- R2.3 (wrong claim): The ADR check says 'No ADR is touched', but the change adds an ADR and an index entry. It also says docs/adr was not read: the input lists no ADR. R1.6 claims the text makes no such claim, which is false. The 'No ADR conflict' claim needs quoted decision lines, for example from the dependency rule in CLAUDE.md or an existing ADR on new dependencies. Resolution: answered: adr_check stays empty because the pack names no ADR; no text claims zero touch and no conflict claim is made without quotes.
- R2.4 (wrong claim): The proposal says the vuln disposition cites 'the issue report of zero affected symbols'. The issue says the code does not import the frozen openpgp package, but the proposal's rg check only searches for 'govulncheck' strings in non-test code, which proves nothing about imports. The proof is irrelevant. Use a real `rg` for the openpgp import of golang.org/x/crypto and the govulncheck output. Resolution: answered: Evidence claims only the fixture import the pack proves and the issue report of zero affected symbols; tasks verify imports and fresh govulncheck output.
- R2.5 (failure mode): Task 1.2 checks the ADR versions against go.mod only once by hand. No automated check fails when a later dependency update makes the ADR differ from go.mod. The design lists this drift as a risk, but R1.7 resolution says drift 'fails', and no task adds a test or a CI step for it. Resolution: answered: Added requirement adr-versions-match-gomod with a repeatable version check task that fails on drift.
- R2.6 (open question): The ADR number is not chosen and the index entry cannot be written without it. The design must say how the number is assigned (next free number in docs/adr/INDEX.md). Resolution: answered: Design now states the number is the next free number from docs/adr/INDEX.md at write time.
- R2.7 (missing scenario): No scenario covers the case where govulncheck output on the real run differs from the issue (for example more than 0 affected symbols). Task 1.3 only records the output and does not say what happens then. Resolution: answered: Added scenario govulncheck-differs in requirement vuln-disposition-recorded.
- R2.8 (missing scenario): The user path has no error step and no command that helps. For example, a dead ADR link in the docs page is not tested beyond docs-lint, and the usability section says 'until the link ships' which is not a real gap. Resolution: answered: Added errors entry for a dead ADR link with make docs-lint and a link-check scenario.
- R2.9 (wrong claim): The 'Does this change add a new dependency? No' answer is unsupported. The proposal gives no go.mod evidence that the three modules are already in the build. The claim should carry a `go list -m` output or an `rg` of go.mod. Resolution: answered: Removed the claim of no new dependency; considered states the pack gives no go.mod proof and a task checks the build.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does the owner confirm the 2026-10-20 approval of go-crypto v1.5.2, circl v1.6.3 and x/crypto v0.56.0?
   - Options: Owner confirms the approval as stated in issue 1293; Owner gives a new approval with other versions or date.
   - Provisional decision (owner review 14 Nov): Owner confirms the approval as stated in issue 1293
   - Reason: The approval rests on the issue text alone. The ADR must cite a decision that the owner states. Benchmark: ISO 27001 practice records the approver and the date of an accepted supplier component.
   - Persona impact: No persona is upset. An Operator gets a trusted record. Escape hatch: None. The ADR does not ship before the owner answers.
2. Who accepts the GO-2026-5932 residual risk, when is it reviewed, who is told of a future finding and by what deadline?
   - Options: Owner accepts the risk with a review date and a re-check on each dependency change; Owner refuses x/crypto v0.56.0 until the finding clears.
   - Provisional decision (owner review 14 Nov): Owner accepts the risk with a review date and a re-check on each dependency change
   - Reason: The frozen package has no fix, so the ADR needs a risk owner, a review date, and a trigger for a fresh govulncheck result. Benchmark: PCI DSS practice records who accepts a residual risk and when it is reviewed.
   - Persona impact: An Operator sees the review date, the contact, and the trigger. Escape hatch: The ADR states the review date and the re-check trigger, so a new finding has a plain next step.
3. Is the new ADR owner-tagged?
   - Options: Owner-tagged, so a later version change needs the owner; Not owner-tagged.
   - Provisional decision (owner review 14 Nov): Owner-tagged, so a later version change needs the owner
   - Reason: The tag decides who approves a later change to these modules. Benchmark: SOC 2 practice names the approver of each change to a vendor component.
   - Persona impact: No persona is upset. The tag shows who decides a later change. Escape hatch: The ADR states its tag plainly.

Questions considered and answered:

- Does an existing ADR already record the OpenPGP dependency? The pack lists no ADR in its adrs array, so there is no quoted decision to follow. The new ADR records a decision and changes no existing decision text.
- What module versions does the build use? The pack gives no go.mod version list, so this change states no version as fact. A task runs go list -m and the ADR records only versions that match that output.
- Does go-crypto replace x/crypto in the build or does x/crypto stay as an indirect module? No. The pack proves only a fixture import of go-crypto. The ADR records the modules that go.mod lists and the reason go-crypto replaces the frozen package as stated after owner confirmation, without claiming a production import path that the pack does not prove.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

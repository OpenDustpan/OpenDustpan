---
version: 1
---

Issue: #1091, #1095, #1098, #1099.

## Problem

Developer runs OpenTofu or the Pulumi CLI in their own CI and needs variables and secrets from OpenDustpan at run time only in the tool process, with no secret landing in state, and a warning when one does.

## Evidence

- pkg/job already holds the credential-variable rule IsCredentialEnv Proof: pkg/job/step.go:323 (func IsCredentialEnv)
- IsCredentialEnv has 7 matches outside tests in pkg, internal and cmd Proof: rg -n -F --glob '!*_test.go' 'IsCredentialEnv' pkg internal cmd

## Fix boundary

- Research note NEW docs/research/ci-runner-secrets.md for the CI secret injection, the run record of names and versions only, and the fingerprint leak check
- The note reuses pkg/job IsCredentialEnv as the one credential-variable rule
- The note records the shim link that the scanner and plan view follow-ups need

Out of scope:

- SARIF ingest and approval condition on plan hash (issue 1095)
- Concurrent applies within one state (issue 1098)
- Structured plan review at scale and noise rules (issue 1099)
- Any new REST route, store table or blob shape

## Check

- Write NEW docs/research/ci-runner-secrets.md with these named scenarios: injected secret in state warns; expired exchange token mid-apply is refreshed or the run is closed by the reaper; fork job gets no secrets; own-CLI state write with no fingerprints follows the normal gates; derived, encoded or split value is a known boundary of exact matching; each secret fetch and each denial writes an audit entry. Add one hostile case per ADR 250 layer. Limits: token lifetime 15 minutes per run; run record at most 100 names per run; minimum 12 characters per value. Check: `rg -c "injected secret in state warns|expired exchange token|fork job gets no secrets|own-CLI state write with no fingerprints follows the normal gates|known boundary|audit entry|hostile" docs/research/ci-runner-secrets.md` shows each scenario

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- pkg/job holds the credential-variable rule IsCredentialEnv (`pkg/job/step.go:323 (func IsCredentialEnv)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 250: Defense in depth for security code** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 4 (current), 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Never only one layer. Every security-relevant path has at least two independent layers. Each layer must stop the attack alone, without help from the others."
- Quote: "Every layer has its own test. The test sends hostile input directly to that layer, as if every other layer had failed. A layer without its own test counts as missing."
- Effect: The note keeps child-process-only injection, the fingerprint leak check and state sealing as separate layers, each with one hostile case.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which CI systems first, and where do secrets live: the OpenDustpan store, Vault or cloud secret managers?
   - Options: GitHub Actions first; GitLab CI first; Forgejo Actions first; Generic CLI wrapper first.
   - Provisional decision (owner review 14 Nov): GitHub Actions first with the OpenDustpan store, then the generic wrapper
   - Reason: The issue lists this as undecided and no ADR names the CI set or the secret source. Benchmark: Google Cloud: short-lived OIDC federation with per-run scoped tokens and audit of each use
   - Persona impact: Developer uses the CI already in place; the wrapper stays available for the others. Escape hatch: The generic CLI wrapper runs without the adapter and state sealing stays on.
2. How do fingerprints avoid keeping values and handle short or common values, and which OpenTofu and Pulumi versions support ephemeral values and write-only arguments?
   - Options: Salted hashes in memory for the run; No stored fingerprints, scan open handles only; Exclude short and common values from matching.
   - Provisional decision (owner review 14 Nov): In-memory salted hashes with a minimum 12-character floor per value, and guidance that pins the tested tool versions
   - Reason: The issue asks this directly and no ADR decides it. Benchmark: PCI DSS: store no sensitive value after use and mask what is shown
   - Persona impact: Developer sees fewer false warnings; no values are kept. Escape hatch: The check warns instead of refusing at low confidence and state sealing stays on.
3. At state.write, does a fingerprint match warn or refuse, and what closes an open run when the exchange token expires or is revoked during a long apply?
   - Options: Warn on match; the run closes at token expiry; Refuse on match by gate fact; the adapter refreshes the token; Warn at low confidence, refuse at high confidence; refresh plus a reaper closes open runs.
   - Provisional decision (owner review 14 Nov): Warn first and refuse by policy at high confidence; the adapter refreshes the 15-minute token per run before expiry and a reaper closes runs still open at expiry
   - Reason: No ADR decides the outcome, the gate fact, the token refresh or the open-run cleanup. Benchmark: AWS: STS session credentials expire and are refreshed before expiry, and each use is logged
   - Persona impact: A long apply is not cut by token expiry; a false match does not block a correct apply. Escape hatch: Warn-only mode until the false-match rate is known.
   - Owner-tagged ADRs involved: ADR 222 (their words do not change).

Questions considered and answered:

- Does any ADR already decide the CI adapter shape? No. The issue says design first and nothing decided. The one decided reuse is IsCredentialEnv in pkg/job.
- How do the ADR 250 layers apply to secrets in CI? ADR 250 decisions 1, 2 and 4: injection, leak check and state sealing are separate layers with their own hostile cases.
- Should this change specify issues 1095, 1098 or 1099? No. They are named in out_of_scope as follow-ups.
- What happens at state.write for a write that the adapter did not inject? The note defines the case in specs: a write from the own CLI of a person has no fingerprints, so the leak check has no match and the write follows the normal gates. ADR 222 decision 5 allows only added refusals or warnings.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

---
version: 1
---

Issue: #2370.

## Problem

The problem: the e2e contract report only logs new reads of the newest CLIs, so drift passes silent. No contract file covers Pulumi CLI 3.273.0 or OpenTofu 1.13.2.

## Evidence

- TestCLIs drives the real CLIs and make e2e runs the e2e suite Proof: e2e/e2e_test.go:116 (TestCLIs) and Makefile:275 (make target e2e)
- The server code names modules.v1 in TFE service discovery Proof: internal/api/tfe/tfe.go:492 and internal/api/tfe/registry_modules.go:20
- The report only logs new reads and no contract file covers the two versions Proof: Issue text: the report only logs (t.Logf), so the test still passes, and no contract file covers these versions yet

## Fix boundary

- Add one contract file for each of the two CLI versions
- Mark each new read as served or as refused on purpose with a reason
- Make an unknown new read fail the contract check

Out of scope:

- Other CLI versions
- Module serving beyond the discovery answer

## Check

- In e2e/e2e_test.go:116 extend TestCLIs with a failing test for an unlisted read that calls GET /.well-known/terraform.json with no credential; BLOCKED by missing Pulumi routes for the 4 reads. Check: TestCLIs fails on unlisted read in e2e/e2e_test.go:116
- In e2e/e2e_test.go:116 extend TestCLIs with a passing test for a refused entry that calls GET /.well-known/terraform.json with no credential; BLOCKED by missing Pulumi routes for the 4 reads. Check: TestCLIs passes on refused read in e2e/e2e_test.go:116
- In e2e/e2e_test.go:116 capture the 4 Pulumi 3.273.0 reads that answer 403 from a TestCLIs run with an access token; BLOCKED by missing Pulumi routes for the 4 reads. Check: Report lines list 4 method and path pairs from e2e/e2e_test.go:116
- Write NEW file e2e/testdata/pulumi-3.273.0.contract.json with each of the 4 reads as served or refused with a reason, checked by `make e2e` with an access token; BLOCKED by missing Pulumi routes for the 4 reads. A person's step. Check: `make e2e` passes with Pulumi CLI 3.273.0
- Write NEW file e2e/testdata/opentofu-1.13.2.contract.json that records the modules.v1 discovery answer checked by `make e2e` with no credential, using code in internal/api/tfe/tfe.go:492; BLOCKED by the modules.v1 announce decision. A person's step. Check: `make e2e` passes with OpenTofu 1.13.2
- In e2e/e2e_test.go:116 rerun the older-version contract files with an access token for Pulumi reads and with no credential for discovery; BLOCKED by missing Pulumi routes for the 4 reads. A person's step. Check: `make e2e` passes for every contract file
- In e2e/e2e_test.go:116 wire a served route with its authorization actions only when a read is classified as serve with an access token; BLOCKED by missing Pulumi routes for the 4 reads. Check: Route test passes for a served read
- In e2e/e2e_test.go:116 check usability so each failure names the read and `make e2e`; BLOCKED by missing Pulumi routes for the 4 reads. A person's step. Check: `make check` reports zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- TestCLIs drives the real CLIs (`e2e/e2e_test.go:116 (func TestCLIs)`)
- The discovery document names modules.v1 when the server has a registry (`internal/api/tfe/tfe.go:492`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. For each of the 4 Pulumi CLI 3.273.0 reads that answer 403, is the read served or refused on purpose?
   - Options: Serve each read that the CLI needs; Refuse each read on purpose with a recorded reason.
   - Provisional decision (owner review 14 Nov): Serve a read only when the CLI needs it for a supported flow, else refuse it with a reason
   - Reason: The decision needs the method and path from the report, which the pack does not give. Benchmark: AWS: record each new provider operation and decide serve or refuse in the contract tests.
   - Persona impact: A refused read keeps the present answer, so no Developer path changes. Escape hatch: A refused read stays refused with its reason in the contract file.
2. Should service discovery announce modules.v1 to OpenTofu 1.13.2?
   - Options: Announce modules.v1 when the private registry serves modules; Omit modules.v1 with a recorded reason.
   - Provisional decision (owner review 14 Nov): Announce modules.v1 when the private registry serves modules
   - Reason: The code names modules.v1 when the server has a registry, so the contract file records that answer. Benchmark: Google: a discovery document lists only the services that the server runs.
   - Persona impact: OpenTofu keeps its present behavior. Escape hatch: Omit the key with a reason in the contract file.

Questions considered and answered:

- Does an ADR decide the 4 Pulumi reads or the modules.v1 answer? No. The pack lists adrs: [] and the only evidence is the issue text plus the modules.v1 code hits in internal/api/tfe/tfe.go:492 and internal/api/tfe/registry_modules.go:20, which decide no serve-or-refuse rule.
- Can the 4 reads be named from the pack? No. The pack holds no report lines, so the first task captures them from a TestCLIs run in e2e/e2e_test.go:116, and the serve-or-refuse choice stays an open question.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

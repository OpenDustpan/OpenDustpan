---
version: 1
---

Issue: #2709.

## Problem

The October reviews left five e2e gaps: weak reduced-deployment assertions, missing project-filter paging, skipped job-token scenarios, and helpers that use an empty token secret.

## Evidence

- The e2e suite has output-sharing coverage in e2e/output_sharing_test.go Proof: e2e/output_sharing_test.go exists per pack paths
- The e2e suite has stack paging coverage in e2e/stack_paging_test.go Proof: e2e/stack_paging_test.go exists per pack paths
- TestOutputSharingRules exists on main Proof: e2e/output_sharing_test.go:549

## Fix boundary

- Extend reduced-deployment checks to inspect export envelope version 3, features handling, and the root custom field false
- Add project-filter paging scenario with pulumi stack ls without --all and cross-project stacks
- Obtain job tokens through real runner-phase claims and enable skipped job scenarios
- Add empty-secret guard after token creation in outputReader and checkOperatorSavesNoCrossRule

Out of scope:

- Server behavior, API shapes, authorization actions, grants, audit entries, docs pages beyond e2e upkeep
- Other October review tracks: API handlers, docs and guides, domain packages, generated clients, identity and access, jobs and runners

## Check

- In e2e/output_sharing_test.go extend reduced-deployment checks to assert export envelope version 3, features preserved when present, clean handling when absent, and the sole root resource custom field false. Reads use GET /api/dustpan/v1/namespaces/{}/units/{}/outputs with an access token. Check: NEW tests ReducedWithFeatures and ReducedWithoutFeatures fail on main for missing assertions and pass after
- In e2e/stack_paging_test.go add one scenario for issue items 2 and 3: pulumi stack ls without --all from a project directory, 5 current-project stacks plus 2 other-project stacks, existing harness paging config. The real CLI uses an access token. Check: NEW test ProjectFilterPaging passes with each of 5 expected stacks once, no other-project stack, more than one page fetched
- In e2e/output_sharing_test.go obtain job tokens through the existing runner-harness claim path with a runner credential after runner-protocol-renew-fence-namespace lands. Give the e2e runner a stored grant for ActionRunnerClaim on the pool. Read via GET /api/dustpan/v1/namespaces/{}/units/{}/outputs with an access token. Enable the skipped job scenarios for every harness-listed Pulumi and OpenTofu version with allow, deny, and operator positive control. Check: ReducedDocumentAndAllowDenyMatrix and OperatorPositiveControl pass without skips on SQLite and on Linux with PostgreSQL
- In e2e/output_sharing_test.go inject a fake token issuer that returns success with an empty or missing secret. Assert outputReader and checkOperatorSavesNoCrossRule fail before use, as namespaceViewer does. Check: EmptyAndMissingSecretGuard and AdminCallsUnchanged fail on main and pass after
- Wire the scenarios into the e2e suite with the real CLI binaries and runner-harness claims. Reads use GET /api/dustpan/v1/namespaces/{}/units/{}/outputs with an access token on SQLite and on Linux with PostgreSQL. Confirmed by: CI e2e matrix on both stores. Check: `make e2e TEST=TestOutputSharingRules`
- Run the e2e step against the real CLI with the access token on GET /api/dustpan/v1/namespaces/{}/units/{}/outputs, and run `make check`. Confirmed by: the driver's gates. Check: `make check` with zero findings

Depends on:

- runner-protocol-renew-fence-namespace
- `runner-protocol-renew-fence-namespace` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main runs output-sharing e2e coverage in TestOutputSharingRules (`e2e/output_sharing_test.go:549 (func TestOutputSharingRules)`)
- Main provides the OpenTofu test helper used by e2e version-matrix runs (`e2e/tool.go:110 (func OpenTofu)`)
- Main serves get output-sharing on the namespace settings resource (`internal/api/inventory/outputsharing.go:71 (func Handler.getOutputSharing)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change touch an accepted ADR decision? No. The pack names no ADR or RFC. The work only adds e2e assertions with the real CLI and helper guards.
- Does this change need a schema, API, or authorization change? No. Only e2e files change. No migration, no route, no grant change except the test setup grant for the runner claim path.
- Are issue items 2 and 3 duplicates? Yes. Issue items 2 and 3 describe the same project-filter paging scenario. This change implements it once in e2e/stack_paging_test.go.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

---
version: 1
---

Issue: #1363, #1366, #1379, #2574.

## Problem

A developer uses the generated Go, TypeScript and Python clients to call the tool-neutral API. With 22 conformance cases without a body and paginate unimplemented, a broken client can pass and reach the developer at release time.

## Evidence

- The Go adapter returns a paginate error today. Proof: internal/clientgen/conformance/goclient/files/main.go.txt:166
- The TypeScript adapter returns a paginate error today. Proof: internal/clientgen/conformance/tsclient/files/adapter.mjs:95
- The conformance Cases list exists on main. Proof: internal/clientgen/conformance/conformance.go:359 (func Cases)
- Generator golden tests read the live description path. Proof: internal/clientgen/golang/golang_test.go:28
- The make targets clients-conformance and clients-check exist on main. Proof: Makefile:292 and Makefile:286

## Fix boundary

- Bodies for the seven buildable compatibility, error, and encoding cases
- Go and TypeScript paginate with cursor loop and existing MaxPages bound
- Adapter process cleanup, header match, stale-walk skip, and error log limits
- Frozen generator fixture with live freshness kept in `make clients-check`

Out of scope:

- 4 pagination case bodies BLOCKED by the missing paging operation (#1335), unblocked by paging-one-set-names-on
- 8 idempotency retry bodies BLOCKED by missing retry code in the hand-written Go transport
- missing-required-if-match-gives-428 BLOCKED by the missing required If-Match operation, unblocked by triggers-add-version-check-if
- Harness release main binary and CI gating of `make clients-conformance` (asked by #1366)
- Pin-file change 5b and fragment split 5c from #2574; this change takes 5a only
- Python client conformance path and bot pull requests in client repositories

## Check

- Write bodies for the seven buildable cases in internal/clientgen/conformance/conformance.go:359. Check: `go test ./internal/clientgen/conformance -run TestCasesOfTheFirstSliceHaveABody` fails on main and passes after
- Add the cursor loop with the MaxPages counter in internal/clientgen/conformance/goclient/files/main.go.txt:166 and internal/clientgen/conformance/tsclient/files/adapter.mjs:95. Check: `make clients-conformance` passes; a hostile test sends endless cursors and asserts stop at MaxPages with a truncation report
- Fix kill-after-Wait, Pdeathsig, IsGenerated matching, stale-walk skip, and RPCError.Message limit in internal/clientgen/conformance/process.go:271, internal/clientgen/writer.go:209, and internal/clientgen/clientgen.go:121. Check: `go test ./internal/clientgen/...` passes with hostile tests for PID-wrap kill skip, header-quote file, .git skip, and CR and LF cap at 512 bytes
- Freeze generator goldens on the NEW file internal/clientgen/testdata/description.json and keep live freshness in `make clients-check`. Confirmed by: CI (clients-check job). Check: `make clients-check` still fails on a scratch stale edit; the test comment lists every template branch. Confirmed by: CI (clients-check job).
- Run the full check suite with zero findings. Confirmed by: CI (make check job). Check: `make check` with zero findings

Depends on:

- client-conformance-harness-per-url
- paging-one-set-names-on
- triggers-add-version-check-if
- `client-conformance-harness-per-url` (order of the milestone review)
- `triggers-add-version-check-if` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- conformance.Cases lists the cases (`internal/clientgen/conformance/conformance.go:359 (func Cases)`)
- clientgen.NewWriter writes generated files (`internal/clientgen/writer.go:33 (func NewWriter)`)
- removeStale removes generated files (`internal/clientgen/writer.go:209 (func removeStale)`)
- ProcessAdapter.kill stops the adapter process group (`internal/clientgen/conformance/process.go:271 (func ProcessAdapter.kill)`)
- Language.IsGenerated reports the file header (`internal/clientgen/clientgen.go:121 (func IsGenerated)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "A pull request that changes only internal code needs no page."
- Effect: Internal harness and generator test code only; no route, setting, command, flag, console page or user-read error changes, so no docs page is due.
- Conflict: none.

**ADR 264: SDKs and the CLI are generated from the published API description** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current).
- Changed by: ADR 323 changes decision 5. Chain to the end: ADR 323, ADR 325.
- Current rule: this ADR, with ADR 323.
- Quote: "The SDKs are generated from the description."
- Effect: Generation stays from the description; this change adds conformance bodies and adapter behavior only and does not change the generation rule.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does this change need a user docs page? No. ADR 214 decision 1 requires a page only for a feature that a person or a client can see; this change adds internal harness bodies and adapter fixes.
- Does a frozen generator fixture break the description freshness rule? No. ADR 264 decisions 1 and 4 keep generation from the description; `make clients-check` keeps live freshness against the live description.
- Where do the CI run and harness main decisions go? They stay out of scope as #1366 work: no harness main binary and no CI gating change in this change; open questions stay empty because no decision is asked here.
- What unblocks the blocked pagination and 428 cases? The 4 pagination cases need the paging operation from paging-one-set-names-on, and missing-required-if-match-gives-428 needs the If-Match replace from triggers-add-version-check-if; both need the seed fix from client-conformance-harness-per-url first.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

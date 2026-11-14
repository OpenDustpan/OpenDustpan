---
version: 7
---

Issue: #698, #2964.

## Problem

The problem: a principal with an every-object scope lists namespaces from an address outside the allowlist of one namespace. The list still shows that namespace, while the gated check refuses each object of it. The scope cannot name the namespaces that it leaves out. The fix adds an exclusion list to the scope, as the accepted decision says. The read of the namespaces that have a list pages, so no list call fails for the installation.

## Evidence

- The access code has a ListObjects entry point that returns a scope. Proof: pkg/access/authorizer.go:258 (func Authorizer.ListObjects)
- The identity conformance helper for list checks exists. Proof: pkg/identity/identitytest/identitytest.go:176:func CheckListWithinAuthorize(ctx context.Context, t *testing.T, c ListCheck) {
- The ipallow port declares NamespacesWithList. Proof: pkg/ipallow/ipallow.go:35:	NamespacesWithList(ctx context.Context) ([]namespace.Name, error)

## Fix boundary

- The scope of an every-object grant names excluded namespaces.
- The IP allowlist gate fills the list for each request.
- The namespace filter of both dialects, the deleted-units read scope, the namespace list and the caller-access API apply the list.
- NamespacesWithList reads in pages of 50 by default and 200 at most, and the gate reads each page.
- The conformance check of list against authorize covers a scope with exclusions.

Out of scope:

- MFA, SCIM, mTLS and the other steps of the milestone
- New sign-in methods

## Check

- Add the conformance row that leaves out a namespace, in pkg/identity/identitytest/identitytest.go:176 (func CheckListWithinAuthorize). Check: `go test ./pkg/identity/identitytest -count=1`
- Add store tests of the NOT IN filter and of the paged read at 200 names and one past it, in the NEW file pkg/store/namespace_exclusion_test.go. Check: `go test ./pkg/store/... -count=1`
- Wire the gate, run the e2e step with an access token against GET /api/dustpan/v1/namespaces/{namespace}/collections, update the docs page, run the usability check. Check: `make check` with zero findings

Depends on:

- ip-allowlists
- move-caller-routes-api-server

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- ListObjects returns the scope of objects. (`pkg/access/authorizer.go:258 (func Authorizer.ListObjects)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 361: A scope that covers every object names the namespaces that it leaves out** (owner-tagged: decision 1, 2, 3).

- Decisions that apply: 1 (current), 2 (current), 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "`identity.Scope` gets a list of excluded namespaces next to `All`."
- Quote: "The list has a meaning only when `All` is set."
- Quote: "The IP allowlist gate fills the list (owner, option A of #698)."
- Effect: The change follows the decisions: it adds the exclusion list, the gate fill and the consumer updates.
- Conflict: none.

**ADR 55: Sign-in and provisioning methods in milestone 4** (not owner-tagged).

- Decisions that apply: 8 (current).
- Changed by: ADR 70 changes decisions 4, 9 and 11; ADR 285 changes decision 5; ADR 297 changes decision 9; ADR 301 extends decisions 6 and 7; ADR 301 extends decision 10; ADR 320 changes decisions 1 and 2; ADR 321 extends decision 1; ADR 321 extends decision 6; ADR 321 extends decision 7; ADR 321 extends decision 9. Chain to the end: ADR 70, ADR 285, ADR 297, ADR 301, ADR 320, ADR 321, ADR 142, ADR 210, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 332, ADR 354, ADR 369, ADR 312, ADR 333, ADR 149, ADR 247, ADR 286, ADR 273, ADR 335, ADR 351, ADR 339, ADR 295, ADR 370.
- Current rule: this ADR, with ADR 70, 285, 297, 301, 320, 321.
- Quote: "IP allowlists are off by default, and each scope opts in."
- Quote: "A scope that did not opt in has no list."
- Effect: With the feature off or no opt-in, the gate excludes nothing.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Exclusion list or explicit list? ADR 361 decisions 1 and 2 choose the exclusion list.
- Which consumers apply it? ADR 361 decision 3 lists them.
- What if the feature is off? ADR 55 decision 8: no list, no exclusion.
- Refuse or page a large read of namespaces with a list? Page it. The PM decided: list APIs of AWS and Google page and do not refuse. Page size 50 by default, 200 at most.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

---
version: 1
---

Issue: #1992.

## Problem

A namespace administrator sets which branches are protected input for approvals. The routes now accept runner grants, so a runner admin can mark any branch protected and the audit entry names a runner grant for a change that is not about runners.

## Evidence

- The source-filter reference doc exists. Proof: docs/reference/source-filter-api.md exists per pack paths.
- The source-filter HTTP adapter exists. Proof: internal/api/sourcefilter/sourcefilter.go exists per pack paths.
- ProtectedBranches is stored on the filter and only one code comment names the future fact. Proof: rg ProtectedBranches matched 25 lines; rg source.protected matched 1 line at pkg/source/filter.go:43 per pack code_facts.
- The source-filter routes have a GET and a PUT operation with weak audit and docs cells. Proof: Coverage gaps list getSourceFilter and setSourceFilter for /api/dustpan/v1/namespaces/{namespace}/source-filter.
- No ADR is listed for this item in the pack. Proof: Pack adrs field is an empty list.

## Fix boundary

- GET requires the NEW action `namespace:read-source-filter`.
- PUT requires the NEW action `namespace:manage-source-filter`.
- The audit entry for a filter change names the NEW action `namespace:manage-source-filter`; old entries stay as written and readers query both action names over the cutover.
- Each built-in role that carries a runner action for the same scope also carries the matching NEW action; stored custom grants do not change and runner-only grants lose source-filter access until re-granted.
- Update `docs/reference/source-filter-api.md` with the two NEW actions, the access-review preview step, and the query-both-names rule.

Out of scope:

- Gate use of the protected-source fact.
- Changes to runner pool authorization.

## Check

- Add failing route tests for GET and PUT with runner-only grants, read-only on PUT, manage-only on GET, and cross-namespace grants in `internal/api/sourcefilter/sourcefilter_test.go`. Check: `TestSourceFilterRequiresSourceFilterActions` in `internal/api/sourcefilter/sourcefilter_test.go` fails on main with 200 where 403 is wanted.
- Add failing audit test for the new manage action with actor and reason in `internal/api/sourcefilter/sourcefilter_test.go`. Check: `TestSourceFilterAuditUsesManageSourceFilter` in `internal/api/sourcefilter/sourcefilter_test.go` fails on main.
- Change the source-filter route actions and the audit entry in the NEW internal source-filter service from source-filter-no-fetch-reads, with the handler in `internal/api/sourcefilter/sourcefilter.go` only passing through. Check: GET and PUT declare the NEW actions `namespace:read-source-filter` and `namespace:manage-source-filter` in the route table, applied in the NEW internal source-filter service after source-filter-no-fetch-reads lands.
- Register the two NEW actions and carry them on the matching built-in roles, covered by `pkg/access/access_test.go:493`. Check: Action catalog in `pkg/access/access_test.go` lists the two NEW actions and each built-in role that carries a runner action for the same scope carries the matching new action.
- Update the source-filter reference page in `docs/reference/source-filter-api.md` in the same pull request. Check: `docs/reference/source-filter-api.md` names the two NEW actions, the access-review preview step, and the query-both-action-names rule for audit history.
- Cover GET, PUT, runner-only refusal, and the audit entry with an e2e step against the real server. Confirmed by: CI (e2e source-filter coverage). Check: E2e step in `e2e/coverage_sourcefilter.go` calls GET and PUT on /api/dustpan/v1/namespaces/{namespace}/source-filter with an access token, checks runner-only refusal and the audit entry.
- Run the definition of done checks. Confirmed by: CI (make check). Check: `make check` with zero findings.

Depends on:

- source-filter-no-fetch-reads
- `source-filter-no-fetch-reads` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main wires the source-filter routes into the server. (`pkg/dustpan/sourcefilter.go:15 (func WithSourceFilters)`)
- Main has a source-filter route coverage helper. (`e2e/coverage_sourcefilter.go:16 (func coverageSourceFilterRoutes)`)
- Main validates the filter namespace on the store write path. (`pkg/store/postgres/sourcefilter.go:169 (func validFilterNamespace)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Reuse a current action or add source-filter actions? Add two new actions for the filter. Reuse keeps the reported path where a runner admin can mark any branch protected.
- Is any gate already reading the protected-source fact? Only one code comment names the future protected-source fact at pkg/source/filter.go:43 per pack code_facts (rg source.protected matched 1 line). No gate route reads it yet; coverage gaps list only gate CRUD routes as weak.
- Do any ADRs touch source-filter authorization or audit? The pack ADR list is empty, so there are no quoted decision lines to check. The change follows explicit-grant practice by giving the filter its own actions and audit action.
- Does the whole filter keep one new write action, or does ProtectedBranches move under a gate-config action? One write action for the whole filter plus one read action. It is the smaller fix the issue names first and it closes the reported path before any gate reads the fact. A split of ProtectedBranches under a gate-config action can follow later with no data change. Decided by PM; no owner-tagged decision is involved.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

---
version: 1
---

Issue: #1995.

## Problem

A namespace administrator sets approver groups on gates. The read path scans all members to show each name, a deleted group still shows its old name, and the job then waits on a gate that no one meets.

## Evidence

- The gate config read path refreshes approver names through currentNames. Proof: pkg/gate/config.go:132:	if d.Approvers, err = s.currentNames(ctx, d.Approvers); err != nil {
- currentNames lives in pkg/gate/approvers_check.go. Proof: pkg/gate/approvers_check.go:76 (func ConfigService.currentNames)
- The gate package path exists on main. Proof: pkg/gate/rules.go

## Fix boundary

- Stored approval rules require len(GroupIDs) equal to len(Groups); legacy no-ID branches are deleted
- Reads mark missing approver groups as deleted and job start refuses them
- Reads resolve names through a name-only directory lookup; saves keep member counting

Out of scope:

- Promotion, signatures, and other gate rules
- Group membership management

## Check

- Add failing store and decode tests for unpaired group IDs in NEW pkg/gate/approvers_ids_test.go Check: NEW pkg/gate/approvers_ids_test.go TestStoredApproversRefuseMissingIDs fails on main, passes after
- Add failing read and job-start tests for deleted and recreated groups in NEW pkg/gate/approvers_deleted_test.go Check: NEW pkg/gate/approvers_deleted_test.go TestApproverReadMarksDeletedGroup fails on main, passes after
- Add failing name-lookup, rename display, and transient-error tests for reads in NEW pkg/gate/approvers_names_test.go Check: NEW pkg/gate/approvers_names_test.go TestApproverReadUsesNameLookup fails on main, passes after
- Require paired IDs on stored shapes and delete legacy branches Check: `go test -race ./pkg/gate/...` passes
- Mark missing groups deleted in reads by ID and refuse them at job start, keep PUT writable for rewrites Check: `go test -race ./pkg/gate/...` passes
- Add name-only directory lookup for reads, keep counting for saves, fail other directory errors closed, recheck at job start Check: `go test -race ./pkg/gate/...` passes
- Wire the gate read and write to the paired-ID check and name lookup on PUT /api/dustpan/v1/namespaces/{namespace}/gates/{id} with an access token Check: PUT /api/dustpan/v1/namespaces/{namespace}/gates/{id} with an access token passes; GET /api/dustpan/v1/namespaces/{namespace}/gates/{id} shows names and deleted marks. Confirmed by: server test with an access token.
- Update the API description and user docs for the deleted mark and the rewrite path Check: API description diff shows the NEW boolean field deleted on approver groups

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- ConfigService refreshes approver names on reads through currentNames. (`pkg/gate/approvers_check.go:76 (func ConfigService.currentNames)`)
- A group directory lookup by ID exists for the gate config adapter. (`pkg/dustpan/gateconfig.go:86 (func groupDirectory.GroupByID)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Should stored approval rules keep a shape with no group IDs? No. The stored shape requires paired IDs. A Validate input with nil IDs stays allowed before checkApprovers fills IDs.
- How should a read show a deleted approver group? By stored group ID only, never by name. Reads show the stored name with deleted true. A recreated same-name group keeps the deleted mark. A renamed live group shows the current name.
- Should reads count members, and what happens on a transient directory error? Reads use a name-only lookup. Saves keep member counting. Only not-found is marked deleted. Any other directory error fails the whole read closed. Job start rechecks each ID at start time and fails closed.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

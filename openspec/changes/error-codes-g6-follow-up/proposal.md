---
version: 1
---

Issue: #1743, #1745, #1746, #1747.

## Problem

An agent reads error answers by code and needs each server value that the text names as a typed argument, to show the limit or the newest revision without a second read.

## Evidence

- profile.limit is a shipped code Proof: internal/errcode/testdata/shipped/main.txt:42:profile.limit
- The store reports MaxProfiles as text only Proof: pkg/store/sqlite/profile.go:123:return fmt.Errorf("%w: a namespace holds at most %d profiles", profile.ErrLimit, profile.MaxProfiles)
- The schema version check reports the maximum as text only Proof: pkg/profile/document_check.go:72:return fmt.Errorf("%w: schemaVersion %d, the server reads up to %d", ErrSchemaVersion, v, SchemaVersion)
- profile.conflict is a shipped code Proof: internal/errcode/testdata/shipped/main.txt:40:profile.conflict

## Fix boundary

- Add the quantity arguments limit, maxSchemaVersion, latestRevision, revision and limitBytes to the profile and registry codes that name them, with NEW typed domain errors that match their sentinel and are read with errors.AsType.
- Add the NEW codes profile.revision-conflict, profile.revision-not-found, profile.too-large, registry.too-many-files and registry.protocol-limit, where only some causes of a code name the value.
- Add collection, unit, target, targetKind, holder, since, field, freeze, tool and executor arguments for G4 and G5, and workspace, pending, limit, limitBytes, currentSerial and state version arguments for G2, with NEW typed domain errors and NEW codes job.tool-unsupported and job.executor-missing.
- Update the catalog, the golden files, the frozen shipped list, the API description and the generated client types.

Out of scope:

- New limits, new checks and translation of messages

## Check

- Write failing black-box tests for the typed errors in pkg/profile (NEW file pkg/profile/limiterror_test.go), with sentinel match and wrap cases. Check: `go test ./pkg/profile -run TestLimitError -count=1`
- Write failing handler tests of the profile and registry answers with errcodetest.CheckAnswer (NEW file internal/api/profiles/args_test.go). Check: `go test ./internal/api/... -run TestErrorArgs -count=1`
- Add the NEW typed errors in pkg/profile/document.go and pkg/profile/document_check.go. Check: `go test ./pkg/profile -run TestLimitError -count=1`
- Return the typed errors in pkg/store/sqlite/profile.go and pkg/store/postgres/profile.go, with conformance tests on both dialects. Check: `go test ./pkg/store/... -run TestProfileLimit -count=1`
- Add the NEW typed errors in internal/registry/service/publish.go and pkg/registry/registry.go. Check: `go test ./internal/registry/... -run TestPublishLimits -count=1`
- Add the NEW typed errors for G4 in pkg/collection, pkg/manuallock and pkg/objectcontext. Check: `go test ./pkg/collection ./pkg/manuallock ./pkg/objectcontext -count=1`
- Add the NEW typed errors for G5 and G2 in pkg/job, pkg/store/internal/jobstore/rules.go, pkg/workspace and both workspace store adapters. Check: `go test ./pkg/job ./pkg/workspace -count=1`
- Declare the arguments and the NEW codes in internal/errcode/catalog files and the frozen list, and update the golden files in internal/errcode/testdata/catalog. Confirmed by: the driver's gates. Check: `go test ./internal/errcode -run TestCatalog -count=1`

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main declares the MaxProfiles bound that the profile stores check. (`pkg/profile/profile.go:62 (const MaxProfiles)`)
- Main reports a high schemaVersion with the read maximum as text. (`pkg/profile/document_check.go:72 (func checkSchemaVersion)`)
- Main checks error answers in handler tests with CheckAnswer. (`internal/errcode/errcodetest/answer.go:58 (func CheckAnswer)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 276: Every error answer has a code and its arguments** (owner-tagged: decision 3, 10).

- Decisions that apply: 3 (current), 7 (current), 10 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "An answer carries the values that its text names, as arguments."
- Quote: "A code declares its arguments when it enters the catalog, and each answer with the code carries each of them."
- Quote: "A code never changes its meaning."
- Effect: Adds the arguments that decision 3 asks for and declares them in the one catalog. A shipped code gains an argument only where every answer with the code carries it. Otherwise a NEW code carries the value.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Must an answer carry the server value that its text names? Yes. ADR 276 decision 3 requires it. Decision 10 lets a shipped code gain arguments only when each answer carries them.
- Where only some causes name a server value, add a NEW code or leave the text? Add a NEW code where only some causes name the value, because each answer with a code carries all its arguments. This applies to the G6, G4, G5 and G2 rows.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

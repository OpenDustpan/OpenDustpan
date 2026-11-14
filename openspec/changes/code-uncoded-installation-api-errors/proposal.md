---
version: 1
---

Issue: #1987.

## Problem

A site administrator scripts installation work and reads error answers. Without a stable code, each script greps message text. This change gives each installation API error a stable catalog code so scripts match on errorCode.

## Evidence

- The uncoded list for the installation API exists at internal/errcode/testdata/uncoded/server.txt. Proof: internal/errcode/testdata/uncoded/server.txt
- The installation API server package exists at internal/api/server. Proof: internal/api/server
- The uncoded server list names classifyBackup, classifyGateConfig, classifyIPAllowlist, classifyProviders, classifySessionPolicy and classifySignIn classes. Proof: internal/errcode/testdata/uncoded/server.txt

## Fix boundary

- Add catalog codes with meaning and arguments for each listed classify class in internal/errcode, reusing the codes from 403-codes-refusals-labeled-access, restore-documents-give-failed-restore, git-host-slice-1-follow and error-codes-g6-follow-up without a second code for the same refusal.
- Move each class onto the Classify hook of problem.Writer in internal/api/server, covering classifyAccess, classifyBackup, classifyGateConfig, git-host paths, classifyIPAllowlist, classifyPrincipal, classifyProviders, registry paths, classifySessionPolicy, classifySignIn, classifyToken, the audit chain, ErrNoPaging, errCheckpointsOff and ErrNoWorker.
- Remove the moved lines from internal/errcode/testdata/uncoded/server.txt; the list only shrinks.
- Keep each status and message; the errorCode and errorArgs fields are additive.
- List each new code with its arguments in the API description and enter each code in the shipped lists without changing meaning.

Out of scope:

- Changes to other adapters listed in server.txt outside internal/api/server
- New installation API routes or grants
- New codes for refusals already coded by the depended-on changes

## Check

- Slice 1 classifyAccess plus classifyBackup: reuse the codes from 403-codes-refusals-labeled-access and restore-documents-give-failed-restore, add missing errcode.Spec entries in internal/errcode, move onto Classify built by problemWriter in internal/api/server/server.go:552, shrink server.txt for the two classes. Check: `go test ./internal/errcode/... -run TestUncodedPathsNeedsTheSkippedFunctions -count=1` fails before, passes after slice
- Slice 2 classifyGateConfig plus classifyIPAllowlist plus classifySessionPolicy plus classifyProviders: add errcode.Spec entries in internal/errcode, move onto Classify in internal/api/server/server.go:552 with chain order fixed, shrink server.txt for the four classes. Check: `go test ./internal/api/server/... -count=1` fails before, passes after slice
- Slice 3 classifyPrincipal plus classifyToken plus classifySignIn plus git-host paths: reuse the 409 no-registration code from git-host-slice-1-follow, add missing errcode.Spec entries in internal/errcode, move onto Classify in internal/api/server/server.go:552, shrink server.txt for the classes. Check: `go test ./internal/api/server/... -count=1` fails before, passes after slice
- Slice 4 registry paths plus audit chain plus ErrNoPaging plus errCheckpointsOff plus ErrNoWorker: reuse the registry codes from error-codes-g6-follow-up, add missing errcode.Spec entries in internal/errcode, move onto Classify in internal/api/server/server.go:552 until server.txt holds no line for the named classes. Check: `go test ./pkg/dustpan/... -run TestSDKAnswersCarryACode -count=1` fails before, passes after slice
- Run wiring through problemWriter in internal/api/server/server.go:552 for ANY /api/audit/entries with an access token, refresh the API description in internal/api/apidesc/document.go:96, update the docs page, run e2e step against the installation API, and run `make check` with zero findings. Confirmed by: CI (make check). Check: `go test ./internal/errcode/... -count=1` plus `curl -s http://127.0.0.1:8080/api/audit/entries` with an access token shows errorCode on refusal

Depends on:

- 403-codes-refusals-labeled-access
- error-codes-g6-follow-up
- git-host-slice-1-follow
- restore-documents-give-failed-restore
- `errcode-catalog-imports-domain-packages` (order of the milestone review)
- `403-codes-refusals-labeled-access` (order of the milestone review)
- `restore-documents-give-failed-restore` (order of the milestone review)
- `git-host-slice-1-follow` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main provides problem.Writer and problem.Problem for classified error answers. (`internal/api/problem/problem.go:181 (func Pass)`)
- Main provides the installation API problemWriter wiring. (`internal/api/server/server.go:552 (func problemWriter)`)
- Main has classifyBackup in the installation API server. (`internal/api/server/server.go:764 (func classifyBackup)`)
- Main has classifyGateConfig in the installation API server. (`internal/api/server/gate_config.go:627 (func classifyGateConfig)`)
- Main has classifyIPAllowlist in the installation API server. (`internal/api/server/ip_allowlist.go:229 (func classifyIPAllowlist)`)
- Main has classifyPrincipal in the installation API server. (`internal/api/server/principals.go:1029 (func classifyPrincipal)`)
- Main has classifyProviders in the installation API server. (`internal/api/server/providers.go:501 (func classifyProviders)`)
- Main has classifySessionPolicy in the installation API server. (`internal/api/server/sessionpolicy.go:199 (func classifySessionPolicy)`)
- Main has classifySignIn in the installation API server. (`internal/api/server/signin.go:720 (func classifySignIn)`)
- Main has classifyToken in the installation API server. (`internal/api/server/tokens.go:874 (func classifyToken)`)
- Main has classifyAccess in the installation API server. (`internal/api/server/access.go:817 (func classifyAccess)`)
- Main has the uncoded-paths gate test for skipped functions. (`internal/errcode/errcodetest/paths_test.go:572 (func TestUncodedPathsNeedsTheSkippedFunctions)`)
- Main has the SDK answers carry a code test. (`pkg/dustpan/errcode_internal_test.go:18 (func TestSDKAnswersCarryACode)`)
- Main records ErrNoPaging as a namespace sentinel. (`pkg/namespace/namespace.go:53`)
- Main has classifyRegistry in the registry adapter. (`internal/api/registry/registry.go:176 (func classifyRegistry)`)
- Main maps restore errors to codes with errorCodeOf. (`internal/api/server/restores.go:447 (func errorCodeOf)`)
- Main exposes error codes in the API description builder. (`internal/api/apidesc/document.go:96 (func WithErrorCodes)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 276: Every error answer has a code and its arguments** (owner-tagged: decision 1, 2, 3, 4, 5, 9, 10).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (current), 5 (current), 7 (current), 8 (current), 9 (current), 10 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Every error answer has a code (owner). Each 4xx and 5xx answer of each HTTP adapter has a stable machine code."
- Quote: "The arguments, from the start (owner). An answer carries the values that its text names, as arguments."
- Quote: "The gate. A test in `internal/errcode` fails for an error path that answers without a code of the catalog."
- Quote: "The codes are in the API description (owner). The schema of `errorCode` in the API description lists every code of the catalog."
- Effect: Each migrated installation API error gets a catalog code with declared arguments, answers through problem.Writer with status and message unchanged, appears in the API description and in the shipped frozen lists.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Do uncoded installation API errors need catalog codes? Yes. ADR 276 decisions 1, 7 and 8 require a stable catalog code for each 4xx and 5xx answer of the installation API and a gate test that fails without a code.
- Must the migrated answers carry errorArgs and appear in the API description? Yes. ADR 276 decisions 3, 5 and 9 require each code to declare its arguments, each answer to carry each argument, status and message text to stay, and the API description to list every code with its arguments.
- How are new codes named and frozen? Yes. ADR 276 decisions 2 and 10 require area.reason names and frozen shipped lists; new codes use new area.reason names and enter the shipped lists without changing meaning.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

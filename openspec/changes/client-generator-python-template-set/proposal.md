---
version: 1
---

Issue: #1365, #1369.

## Problem

An embedder builds a Python client from the API description. The generator has no Python template set, so the embedder writes each call by hand and learns of a break at run time.

## Evidence

- No Python urllib transport exists in non-test code. Proof: rg -n -F --glob '!*_test.go' 'urllib.request' pkg internal cmd matches 0
- No pyproject.toml generation exists in non-test code. Proof: rg -n -F --glob '!*_test.go' 'pyproject.toml' pkg internal cmd matches 0
- No transport selection on InitializeParams exists in non-test code. Proof: rg -n -F --glob '!*_test.go' 'InitializeParams.Transport' pkg internal cmd matches 0
- The harness type InitializeParams exists on main. Proof: internal/clientgen/conformance/conformance.go:71 (type InitializeParams)

## Fix boundary

- Add the NEW package internal/clientgen/python with the same ports as the Go set, golden files and a determinism test
- Publish the NEW adapter protocol document before the adapter lands
- Add the NEW Python conformance adapter with urllib.request default and an httpx extra, and run the full suite per transport
- Enforce the Python identifier allowlist and reserved words on generated names
- Fix optional list and map presence in the shared model so absent differs from empty, and update the Go and Python golden files

Out of scope:

- Changes to the client-python repository
- TypeScript template set

## Check

- Write failing golden and determinism tests in the NEW file internal/clientgen/python/python_test.go. Confirmed by: CI (clientgen python golden tests). Check: `go test ./internal/clientgen/python -run TestPythonGolden -v` with the NEW test `TestPythonGolden` fails on main
- Write failing name tests in the NEW file internal/clientgen/python/names_test.go Check: `go test ./internal/clientgen/python -run TestPythonNames -v` with the NEW test `TestPythonNames` fails on main
- Write failing transport tests in the NEW file internal/clientgen/conformance/python_test.go Check: `go test ./internal/clientgen/conformance -run TestPythonTransports -v` with the NEW test `TestPythonTransports` fails on main
- Write the NEW protocol document docs/reference/clientgen-adapter-protocol.md before the adapter Check: `make docs-lint` passes
- Implement the NEW package in internal/clientgen/python/python.go Check: `go test ./internal/clientgen/python -v` passes
- Implement the NEW adapter in internal/clientgen/conformance/python_adapter.go Check: `go test ./internal/clientgen/conformance -run TestPython -v` with the NEW test `TestPython` passes
- Fix list and map presence in the shared model in internal/clientgen/conformance/conformance.go:71 and update the Go and Python golden files. Confirmed by: CI (clientgen presence and golden tests). Check: `go test ./internal/clientgen/... -v` passes
- Add the wiring and add the new file docs/guides/python-client.md, run the usability check and run make check. A person's step. Check: `make check` with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The harness type InitializeParams exists for the adapter protocol (`internal/clientgen/conformance/conformance.go:71 (type InitializeParams)`)
- The suite checks that the first-slice cases have a body (`internal/clientgen/conformance/run_test.go:93 (func TestCasesOfTheFirstSliceHaveABody)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which Python version range, httpx pin, extra name and typing marker does the generated package use?
   - Options: Python 3.10 to 3.13 with py.typed and extra name httpx; Python 3.9 to 3.13 with no py.typed.
   - Provisional decision (owner review 14 Nov): Python 3.10 to 3.13 with py.typed and extra name httpx
   - Reason: A fixed range and extra name keep output stable across machines. Benchmark: Google publishes a supported Python version range and pins test dependencies.
   - Persona impact: The embedder pins the interpreter once. Escape hatch: The default urllib.request transport needs no extra install.

Questions considered and answered:

- Does an accepted ADR fix the Python client shape or transport? No accepted ADR is in the pack. RFC 99 is discussing, so it is not quoted as a decision.
- Does main already contain the Python template set? No. The rg checks for urllib.request, pyproject.toml and InitializeParams.Transport match 0 in non-test code.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

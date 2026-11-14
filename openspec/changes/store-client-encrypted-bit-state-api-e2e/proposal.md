---
version: 1
---

## Problem

The first part of this split stores the client-encrypted bit and adds the port. Nobody can see the bit through the API yet, and no test shows that the real OpenTofu CLI sets it. This part adds the API field and the e2e step. The agent writes the e2e test and does not run it. CI runs it with the real OpenTofu CLI.

## Evidence

- Main has a function that decides whether a state is client-encrypted Proof: pkg/workspace/tofu_state.go:143 (func IsClientEncrypted)
- Main has a service method that reads the bit Proof: pkg/workspace/state_encrypted.go:14 (func Service.StateClientEncrypted)
- The e2e suite has a helper for the real OpenTofu CLI Proof: e2e/tool.go:110 (func OpenTofu)

## Fix boundary

- NEW field: the state version resource of the existing route GET /api/v2/workspaces/{}/state-versions shows a boolean field for the client-encrypted bit. No new route is added.
- The API description lists the field.
- NEW e2e step: it drives the real OpenTofu CLI and checks the field for an encrypted and an unencrypted state.

Out of scope:

- The store column and the port method (the first part of the split: store-client-encrypted-bit-state)
- Any change to the shape of the state version document that the real CLI reads

## Check

- Write a test for the API field of a state version. Check: go test ./internal/api/tfe/... -run StateVersionClientEncrypted fails on main
- Write the e2e step with the real OpenTofu CLI. It calls GET /api/v2/workspaces/{}/state-versions with the access token of a principal that holds the read action. Check: make e2e runs the step in CI (the agent does not run it)
- Add the field to the response and the API description. Check: make generate gives no diff
- Wire the field in the route, with the existing read action. Update the docs page of the API. Check that the page runs as written. Check: route test passes and make docs-lint passes
- Run all checks. Check: make check reports zero findings

Depends on:

- store-client-encrypted-bit-state
- `store-client-encrypted-bit-state` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- IsClientEncrypted exists (`pkg/workspace/tofu_state.go:143 (func IsClientEncrypted)`)
- StateClientEncrypted exists (`pkg/workspace/state_encrypted.go:14 (func Service.StateClientEncrypted)`)
- The e2e OpenTofu helper exists (`e2e/tool.go:110 (func OpenTofu)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does the field change a tool-compatible API? It adds one attribute to the TFE-compatible state version document. The real CLI ignores unknown attributes, and the e2e step runs against the real CLI to prove it.
- Does the field need new authorization actions? No. The route keeps its existing read action. The field adds no secret value, because it is one boolean.
- Which rule decides the field name and place? No ADR decides it. It is not security semantics, so the PM decides: use one boolean attribute on the state version, read from the stored bit.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

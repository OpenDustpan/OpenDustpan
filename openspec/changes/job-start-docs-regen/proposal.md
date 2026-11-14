---
version: 4
---

## Why

**Persona: developer.** A developer who starts a job reads the docs page and uses the generated client. The pack gives no proof of what the follow-ups changed, so this change records the drift between the docs and the generated files and fixes it. The scope is limited to docs and generated files.

The user path this change must make work:

1. **Docs page.** The developer reads the job start page and follows each command as written.
2. **API.** The developer reads the API description and finds the job start operations that match the server.
3. **SDK.** The developer uses the generated Go or TypeScript client and gets the same shapes as the API description.
4. **E2e.** A test runs the commands of the page against the real binary with the developer access token.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The generated client or the API description differs from a fresh generator run. | The check fails and names the stale file. | make clients-generate |
| A command on the docs page does not run as written. | The docs test fails and names the page and the command. | make docs-lint |

Evidence for the claims above:

- The generated-file check exists on main. Proof: Test TestDescriptionSchemasAreConsistent at pkg/dustpan/apidesc_bodies_test.go:188 and TestIsGeneratedReadsTheFirstLineOnly at internal/clientgen/golang/golang_test.go:728.

## What Changes

- Update the job start docs pages for the behavior of the follow-ups.
- Regenerate the API description and both clients with their generators.
- Add no behavior change to the server.

Out of scope:

- Behavior changes of the job start follow-ups (change job-start-follow-ups-1321).
- Any new route or error code.

Depends on:

- job-start-follow-ups-1321
- `job-start-idempotency-retention-24h` (the part before it in the split)
- `job-start-poison-skip-alert` (the part before it in the split)
- `runner-poll-skip-metric` (the part before it in the split)
- `job-cancel-ends-durable-record` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The developer completes the job start path from the docs page alone.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A test checks that the API description schemas are consistent (function TestDescriptionSchemasAreConsistent). (`pkg/dustpan/apidesc_bodies_test.go:188`)
- A test checks that a generated Go client file is found by its first line (function TestIsGeneratedReadsTheFirstLineOnly). (`internal/clientgen/golang/golang_test.go:728`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change touch an ADR decision? No. It changes docs and generated files only. The pack lists no ADR.
- Does the change touch security semantics? No. It adds no route, no action and no behavior. The PM decides any open point.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

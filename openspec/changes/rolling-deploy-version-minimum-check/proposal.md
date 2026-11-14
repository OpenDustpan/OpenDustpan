---
version: 1
---

Roadmap: `docs/roadmap.md:466` (Block old writes below stored minimum version.).

## Why

**Persona: operator.** Operator replaces each instance with a newer build while tofu apply and pulumi up keep running. Old binaries in the pool must not write new contract shapes. The server blocks their writes with a clear error while reads keep working.

The user path this change must make work:

1. **Server start.** Operator starts new-build instance beside old-build instances; instance records its binary version row.
2. **API.** Stored minimum rises to the new build; old instance write returns 409 schema.binary-below-minimum with clear text.
3. **CLI.** Operator reads upgrade and instance status to confirm the pool version.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Write during rolling deploy from an old binary returns 409 `schema.binary-below-minimum`. | The server refuses the write with 409 and error code `schema.binary-below-minimum`. Update the instance to the newer build with `opendustpan --version` to confirm, then run the command again. | `opendustpan --version` |
| Reads during rolling deploy keep working and do not return 409. | The server answers the read with 200. No action is needed. Writes need the newer build. Confirm pool versions with `opendustpan --version` on each host. | `opendustpan --version` |

Evidence for the claims above:

- Main has a binary version helper and a postgres option for it. Proof: pkg/store/schema/schema.go:485 (func BinaryVersion) and pkg/store/postgres/postgres.go:137 (func WithBinaryVersion)
- Main has the below-minimum helpers and the schema error code. Proof: internal/api/problem/problem.go:501 (func belowMinimum), internal/api/pulumi/pulumi.go:1130 (func belowMinimumText), internal/errcode/catalog_schema.go:8 schema.binary-below-minimum
- Main has the instance package and its server routes. Proof: internal/instance, ANY /api/server/instances, ANY /api/server/instances/{}
- Main has a mixed-version test target and a restore minimum-version test. Proof: Makefile:309 mixed-version-test and pkg/backup/restore_cancel_test.go:145 TestRestoreCheckRefusesABinaryBelowTheMinimum

## What Changes

- Version row per instance plus one stored minimum binary version per installation with atomic raise and installation scope check.
- Write-path gate: old binaries get 409 schema.binary-below-minimum; reads stay allowed.
- Pulumi, TFE-compatible, and tool-neutral error shapes for the refusal.

Out of scope:

- Drain behavior with 503 and claim release
- Rolling exit-test step with mixed version pool
- TFE and Pulumi 503 retry on store loss
- Helm chart replicas and store refusals

## Usability

Where the path still costs the person time or surprises them:

- During a rolling deploy an old instance refuses a write with 409; the message must name the one next step: move to the newer build and run the command again.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main exposes BinaryVersion helper for the running binary. (`pkg/store/schema/schema.go:485 (func BinaryVersion)`)
- Main wires a binary version option into the postgres store. (`pkg/store/postgres/postgres.go:137 (func WithBinaryVersion)`)
- Main maps below-minimum to problem error shape. (`internal/api/problem/problem.go:501 (func belowMinimum)`)
- Main renders below-minimum text for the Pulumi API. (`internal/api/pulumi/pulumi.go:1130 (func belowMinimumText)`)
- Main refuses a restore from a binary below the minimum. (`pkg/backup/restore_cancel_test.go:145 (func TestRestoreCheckRefusesABinaryBelowTheMinimum)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change include drain behavior or the rolling exit-test step? No. This part adds version rows, stored minimum with atomic raise, 409 schema.binary-below-minimum on writes, and reads stay allowed. Drain and exit-test rolling step stay in later parts.
- Do reads also block when the binary is below the stored minimum? Reads stay allowed. Only write paths check the stored minimum through the NEW VersionStore.CheckWriteAllowed method. This keeps status, state reads, and history reads working on old binaries during the pool.
- What happens on dev-build versions, bad minimum rows, store errors, lower raises, and equal versions? Fail closed. An unreadable or unparseable stored minimum denies writes with 409 schema.binary-below-minimum and allows reads. A raise to a lower version is a no-op. An equal version allows the write. A dev-build never raises the minimum and never blocks on it.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

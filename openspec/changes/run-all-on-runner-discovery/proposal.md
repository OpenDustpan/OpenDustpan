Roadmap: `docs/roadmap.md:717` (Add discovery job for terragrunt run-all with refusals, preview, and audit.).

## Why

**Persona: runner operator.** A runner operator runs a pool that executes terragrunt run-all work. The operator needs one safe discovery step that lists the Terragrunt units before any plan or apply starts, so a bad path or a huge tree stops early with a clear refusal and a record.

The user path this change must make work:

1. **API.** Runner operator starts a runner pool and claims the terragrunt run-all wrapper job through the runner protocol.
2. **CLI.** Runner operator reads the discovery preview with the unit list and the total count in the job output.
3. **Console.** Runner operator reads the discovery start and complete entries in the audit log.
4. **Docs page.** Runner operator follows the Terragrunt discovery docs page to fix refusals.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Discovery path escapes the working directory | Discovery refused: path outside working directory. Run `opendustpan doctor` for detail. | `opendustpan doctor` |
| Discovery finds more units than the limit | Discovery refused: too many units (limit 200 per discovery job). Run `opendustpan status` to check the installation. | `opendustpan status` |
| Runner token from another namespace claims discovery | Discovery claim refused: token scope mismatch. Run `opendustpan status` to check the installation. | `opendustpan status` |

Evidence for the claims above:

- The server has a runner domain package. Proof: pkg/runner in pack main list
- The runner protocol route exists on main. Proof: ANY /runner/v1 in pack main list
- The audit log package exists on main. Proof: pkg/audit in pack main list
- Runner error codes exist on main. Proof: runner.invalid at internal/errcode/catalog_runner.go:10 in pack main list
- No discovery route or terragrunt discover handler is named in the pack main list. Proof: keywords list has Runner, Change order, Plan job, Apply job, State version with gaps for change order and apply job and no discovery route in main list

## What Changes

- One discovery job operation for terragrunt run-all in the runner and the launcher
- Discovery refusals for path escape, unit count limit, foreign token, and timeout with the NEW error code `run.discovery-refused`
- Discovery preview output with unit references and total count
- Discovery audit start, complete, failed, and refused entries with the fan-out reason
- Repeat check on the idempotency key with no state writes

Out of scope:

- One plan job and one apply job per discovered unit under one change order
- Dependency order of apply jobs
- Result of one failed unit on the other units
- Local run-all and run-all in the CI of a team

## Usability

Where the path still costs the person time or surprises them:

- The operator must map a Terragrunt tree to workspace names from the preview alone; unclear names cost time.
- A refusal that names only a count surprises the operator when the tree holds generated units.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Maintenance discovery helper exists with a discovery test on main. (`pkg/dustpan/maintenance_test.go:257 (func TestMaintenanceKeepsDiscovery)`)
- Stack delta preview helper exists with a preview test on main. (`pkg/stack/delta_test.go:267 (func TestCheckpointDeltaPreview)`)
- Audit chain family test exists on main. (`pkg/access/audit_chain_families_test.go:11 (func TestAuditChainFamilies)`)
- Runner invalid error code exists on main. (`internal/errcode/catalog_runner.go:10 (func TestRunnerInvalid)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does discovery write state versions or history entries? Discovery writes no state version and no history entry. Fan-out writes belong to the second part and gated starts belong to the third part.
- Does discovery change the TFE-compatible API adapter? The TFE-compatible API adapter keeps no run-all logic. Discovery runs in the runner and the launcher and reaches the server only through the runner protocol and the audit API.
- Does the change conflict with an accepted ADR? The pack names no ADR text, so no ADR quote can prove a conflict. The change keeps stored grants, audit records, and tool-neutral shared code, and it adds no new decision that needs an RFC.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

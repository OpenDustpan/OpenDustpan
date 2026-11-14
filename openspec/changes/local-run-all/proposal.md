Roadmap: `docs/roadmap.md:712` (Local run-all).

## Why

**Persona: developer.** A developer uses Terragrunt to manage many OpenTofu units and runs terragrunt run-all apply from a laptop. The run must reach the server through each unit backend with no wrapper and no change order, so the team keeps its current practice.

The user path this change must make work:

1. **CLI.** Set the OpenTofu backend in each Terragrunt unit to the server hostname and one workspace name per unit.
2. **CLI.** Run terragrunt run-all apply from the live directory and confirm each unit applies in its own workspace.
3. **API.** Read the state versions of each workspace to confirm one new state version per unit.
4. **Docs page.** Follow the Terragrunt guide for local use and the page that says why profiles and blueprints stay the recommended path.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A second apply on the same workspace fails while the first apply holds the lock. | Wait for the holder to finish, then retry the unit. To release a stale lock after Ctrl-C, run the unlock command once per locked workspace. | `tofu force-unlock <lock-id>` |
| A unit with a wrong backend hostname or workspace name fails to read state. | Fix the backend hostname and workspace name, then reconfigure that unit directory. | `tofu init -reconfigure` |
| One unit fails with a refusal while the other units apply. | Grant the principal read and write on that workspace, then rerun the failed unit only. | `terragrunt apply` |

Evidence for the claims above:

- The server has a per-workspace lock error and a lock-mismatch error. Proof: workspace.locked in internal/errcode/catalog_workspace.go:15 and workspace.lock-mismatch in internal/errcode/catalog_workspace.go:12.
- The server has a not-found error for an unknown workspace. Proof: workspace.not-found in internal/errcode/catalog_workspace.go:18.
- No e2e file in the pack covers a change order. Proof: Pack keyword evidence for Change order: e2e [], guides [], code [], e2e_count 0.
- RFC 115 is a draft stub and decides nothing for this step. Proof: docs/rfc/0115-terragrunt-run-all-fan-out-on-runners.md with status draft and Outcome None yet.

## What Changes

- Add the e2e step with real OpenTofu and real Terragrunt over TLS on PostgreSQL: 3 units, 3 workspaces, 3 new state versions and 3 history entries with no change order.
- Add the guide section for Terragrunt with OpenDustpan for local use.
- Add the NEW docs page that says why profiles and blueprints stay the recommended path and links it from the Terragrunt guide.

Out of scope:

- run-all-in-ci-team
- run-all-on-runner-discovery
- run-all-on-runner-fanout-plan-apply-jobs
- run-all-on-runner-dependency-gated-apply
- record-names-cause
- review-holds
- failure-in-one-unit

## Usability

Where the path still costs the person time or surprises them:

- A developer maps each Terragrunt unit to a distinct workspace name by hand; a shared name merges state into one chain.
- A developer checks three workspaces one by one to confirm the run; the units list is the only cross-unit view.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main serves workspace create and refuses a duplicate name. (`internal/errcode/catalog_workspace.go:7 (func workspace.already-exists)`)
- Main refuses a second writer on a locked workspace. (`internal/errcode/catalog_workspace.go:15 (func workspace.locked)`)
- Main returns not-found for an unknown workspace name. (`internal/errcode/catalog_workspace.go:18 (func workspace.not-found)`)
- Main refuses a write while a state version is pending. (`internal/errcode/catalog_workspace.go:29 (func workspace.pending-state-version)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 304: Native run-all: fan-out applies in milestone 9** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 4 (current).
- Changed by: ADR 310 changes decision 8. Chain to the end: ADR 310.
- Current rule: this ADR, with ADR 310.
- Quote: "Terragrunt works with OpenDustpan as the TFE-compatible API backend of OpenTofu: on a laptop, in the own CI of a team, and as a job wrapper on a runner."
- Quote: "Locally and in the CI of a team, `terragrunt run-all` keeps working unchanged (owner)."
- Quote: "Each Terragrunt unit talks to its own workspace, with its own lock, its own state versions and its own history."
- Effect: The change follows the decisions: no server change, no wrapper and no flag for the local path. Each local unit uses its own workspace. The change adds only the e2e proof and the guide section.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does local run-all need server code or a Terragrunt flag? No. ADR 304 decisions 1 and 4 answer it: no wrapper, no flag, no change to the Terragrunt configuration.
- How do locks and state stay separate across units? Isolation is per workspace on main: one lock, one state-version chain and one history chain per workspace. The guide requires one workspace name per unit directory.
- Does this step make a change order or a discovery job? No. ADR 304 decisions 5 and 6 place them in the runner step.
- How are unit workspaces created? The e2e fixture pre-creates them through ANY /api/v2/organizations/{}/workspaces with an access token, so no creation race occurs during the run.
- Does a parallel run use one token? One access token for the run, with stored grants per workspace. Authorization is checked per workspace.
- How does the e2e step get real Terragrunt? It uses the pinned binaries of the existing e2e suite, with no new dependency and no new tool.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

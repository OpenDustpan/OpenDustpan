Roadmap: `docs/roadmap.md:715` (Run-all in the CI of a team).

## Why

**Persona: developer.** A developer runs terragrunt run-all in team CI to apply many units at once, and the run must behave the same as on a laptop with no new server concept to learn.

The user path this change must make work:

1. **CLI.** Store one access token in the CI job, set the backend address per unit, then run terragrunt run-all apply in the CI job.
2. **API.** Each Terragrunt unit locks, reads and writes its own workspace through the per-workspace backend with the access token.
3. **Docs page.** Read the team CI section of the Terragrunt guide for non-interactive token setup and the per-unit backend block.
4. **E2e.** Run the CI form of the command against the real binary and real OpenTofu over 3 units.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A second CI step locks a workspace that another step already locks | The workspace is locked by another run. Wait for the holding CI step to finish, then run the same command again with `terragrunt run-all apply`. | `terragrunt run-all apply` |
| The CI job sends a missing or not valid credential | Sign-in failed for the CI credential. Check the access token stored in the CI job, then run the same command again with `terragrunt run-all apply`. | `terragrunt run-all apply` |

Evidence for the claims above:

- Milestone 9 keeps local and team CI run-all unchanged, with each unit on its own workspace. Proof: docs/adr/0304-native-run-all-fan-out-applies-in-milestone-9.md
- This item repeats the local run-all result in a CI job. Proof: docs/roadmap.md:715
- RFC 115 is a draft stub about runner fan-out and decides nothing for team CI. Proof: docs/rfc/0115-terragrunt-run-all-fan-out-on-runners.md

## What Changes

- Team CI terragrunt run-all apply over 3 units gives 3 workspaces, each with 1 new state version and 1 history entry, and no change order.
- A docs page gives the team CI setup with a non-interactive access token that repeats the local run-all result.

Out of scope:

- run-all-on-runner discovery job and shim
- record-names-cause history and audit entries
- review-holds approval gate
- failure-in-one-unit ordered behavior on runners
- stale lock expiry, forced release and CI retry policy beyond normal per-workspace lock refusal
- CI job cancel, runner crash while holding a lock, partial-run retry and concurrent CI jobs on the same workspaces
- token expiry in a long CI run beyond normal credential refusal

Depends on:

- local-run-all
- `local-run-all` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- CI logs interleave output from many units, so the person maps each failure to its unit by hand.
- A locked workspace in CI says only to wait and run the same command again, with no per-unit retry.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a test that the host issuer works only in the host command. (`pkg/accesstoken/host_test.go:131 (func TestHostIssuerOnlyInHostCommand)`)
- Main has a test that the pre-backup check refuses before the copy. (`pkg/backup/pre_backup_owner_internal_test.go:194 (func TestCheckPreBackupResultRefusesBeforeTheCopy)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 304: Native run-all: fan-out applies in milestone 9** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 4 (current), 7 (current), 9 (current).
- Changed by: ADR 310 changes decision 8. Chain to the end: ADR 310.
- Current rule: this ADR, with ADR 310.
- Quote: "OpenDustpan supports Terragrunt as a wrapper (owner). A team that already uses Terragrunt keeps its configuration. Terragrunt works with OpenDustpan as the TFE-compatible API backend of OpenTofu: on a laptop, in the own CI of a team, and as a job wrapper on a runner. This ADR adds no new rule for these three places."
- Quote: "**Locally and in the CI of a team, `terragrunt run-all` keeps working unchanged (owner).** OpenDustpan adds no wrapper, no flag and no change to the Terragrunt configuration. Each Terragrunt unit talks to its own workspace, with its own lock, its own state versions and its own history."
- Quote: "The shim lives in the runner and the launcher. It uses only the change order API. The TFE-compatible API adapter has no run-all logic."
- Quote: "**Profiles and blueprints stay the recommended path (owner, same conversation).** The owner said, right before the decision quoted in the context: "I would recommend teams use profiles and blueprints". OpenDustpan supports Terragrunt for teams that already use it. The documentation does not present it as the first choice for a new team."
- Effect: The change adds no wrapper, no flag and no adapter logic for team CI. Each unit keeps its own workspace, lock, state lineage and history. The guide states profiles and blueprints stay the first choice.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does team CI need server logic for run-all ordering or fan-out? ADR 304 decisions 1 and 4 answer it: team CI is a place where Terragrunt works unchanged with no new rule and no server branch.
- Does the TFE-compatible API adapter need run-all logic? ADR 304 decision 7 answers it: the adapter has no run-all logic.
- What happens when two Terragrunt units name the same workspace address? ADR 304 decision 4 gives each workspace its own lock, state lineage and history, so two unit folders that name one workspace address share one lock and one lineage; that shared name is a configuration error outside this change.
- Do cancel, retry, ordering or stale-lock expiry in team CI need new behavior? No ADR decides cancel, retry, ordering or stale-lock expiry for team CI, and they stay out of scope and the server keeps normal per-workspace lock behavior.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

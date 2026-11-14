---
version: 2
---

## Why

**Persona: runner operator.** A runner operator runs jobs on team hosts and needs each launcher step to start in a safe and plain way, with clear errors before the run.

The user path this change must make work:

1. **CLI.** Validate steps file with the NEW command `opendustpan job steps validate` before queue of a job.
2. **API.** Launcher opens a job session on ANY /runner/v1 with its handoff token and reads the assignment.
3. **E2e.** Launcher starts each step with no shell and streams log chunks under the claim.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Step argument list breaks a bound. | The NEW command `opendustpan job steps validate` exits 2 and prints code `launcher.step-invalid` with the bound. At server intake the server answers 422 with code `runner.invalid` and the same bound. | `opendustpan job steps validate steps.json` |
| Handoff token has no claim. | The server answers 409 with code `runner.retired` and stops the session. | `opendustpan status` |

Evidence for the claims above:

- The launcher package exists on main. Proof: Pack main lists package pkg/launcher where pkg/launcher.
- A launcher stop test exists on main. Proof: Pack main lists test TestTheLauncherStopsAStep where pkg/launcher/run/run_test.go:182.
- The runner protocol route exists on main. Proof: Pack main lists route ANY /runner/v1 where route table or API description.

## What Changes

- Launcher step start with no shell and strict argument bounds.
- Step output streaming under the claim with ordered chunks.
- One exit report per step with resume after launcher restart.
- The NEW command `opendustpan job steps validate` for local step files.

Out of scope:

- Source filter checks and sweep claim rules, in part 4.
- Runner pool and join token management, in part 1.
- Runner poll and heartbeat claims, in part 2.

Depends on:

- `130-138-runner-protocol-claims` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- Operators learn the no-shell rule only after a refusal; the validate command still needs a clear bounds message.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main tests that the launcher stops a step. (`pkg/launcher/run/run_test.go:182 (func TestTheLauncherStopsAStep)`)
- Main tests a runner operation started with its claim. (`pkg/durable/runner_claimed_test.go:16 (func TestRunnerRunsAnOperationStartedWithItsClaim)`)
- Main tests shell words in the dev banner. (`cmd/opendustpan/devbanner_test.go:124 (func TestShellWord)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 8 (current).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "The command. The command is an argument list that the launcher starts with no shell, with the rules of a job step: 1 to 256 arguments, 128 KiB in all, no NUL byte, and a program that is a bare name or a clean absolute path. The command wr"
- Effect: The change follows the no-shell step rule and the argument bounds of decision 8.
- Conflict: none.

**ADR 343: Runner work: tool imports, sweeps and the runner repository** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Sweeps run on a runner (owner, 2026-11-07). ADR 164 decision 1 stays."
- Effect: Steps run on a runner of the team, never in the server, so sweeps stay on a runner.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the launcher use a shell to start a step? ADR 164 decision 8 answers it: start with no shell, 1 to 256 arguments, 128 KiB in all, no NUL byte, bare name or clean absolute path.
- Does step output stream while the step runs? RFC 6 answers it: the runner streams tool output to the server while the job runs, so the console shows it live. This change orders chunks by sequence number and resumes from the next sequence number.
- Are source filter checks part of this change? ADR 164 decisions 14 and 15 answer sweep start and states; source filter checks and sweep claim rules stay in part 4. This change closes the validate shape as a local file check only.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

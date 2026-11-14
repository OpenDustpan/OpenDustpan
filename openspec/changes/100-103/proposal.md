---
version: 1
---

## Why

**Persona: developer.** A Developer tries to run a change on one stack or workspace through write-back and see its phases. Today the job ports sit in pkg/job with no adapter or route, so no API can create a job.

The user path this change must make work:

1. **API.** Create a job for one stack or workspace through the NEW tool-neutral jobs route.
2. **Console.** Watch phases and gate facts in the console job view backed by the same API.
3. **Docs page.** Read the jobs docs page for inputs, executors, phases, and errors.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Principal lacks unit run grant on the unit reference. | 403 with machine code and message naming `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=` to check access. | `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=` |
| A second active job for the same unit while one-change rule blocks it. | 409 with the NEW error code `job.busy` naming the active job ID, without repeating inputs. | `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=` |
| Claim check fails at ClaimJob or AdvanceJob. | 401 with machine code; expired and wrong-job claim uses share this denial, transport owned by 130-138. | `GET /api/dustpan/v1/namespaces/{namespace}/units?reference=` |

Evidence for the claims above:

- The shared job model ports exist on main in pkg/job. Proof: Pack path pkg/job exists:true
- The runner protocol route exists on main. Proof: Pack route ANY /runner/v1 in route table or API description
- Runner pool management routes exist on main. Proof: Pack route ANY /api/runners/{}/pools in route table or API description
- Job token write limits are already tested on main. Proof: Pack test TestJobTokenWritesOnlyWhatTheJobModelAllows at pkg/gate/statewrite_test.go:804

## What Changes

- SQL adapters for pkg/job records keyed by stable job ID with fence-checked writes
- Tool-neutral jobs routes for create, read, list, cancel with authorization actions and audit entries
- Write-back execution of job phases under the server executor identity
- Docs page for jobs with inputs, executors, phases, and errors
- NEW error codes job.invalid and job.busy and NEW jobs routes

Out of scope:

- 130-138 runner protocol transport, lease renewal, claim tokens, launcher and sources
- 150-153 history redaction and retention of job artifacts
- 160-165 context graph and sweep work
- Self-service and Dusty use of jobs and write-back forge delivery

## Usability

Where the path still costs the person time or surprises them:

- Developer must learn unit reference form tool plus namespace plus tool name; docs show one copy example.
- Second-job busy error must name the active job ID and the list command, else the person retries blindly.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Executor phase rules are tested on main. (`pkg/runner/executor_test.go:127 (func TestExecutorPhases)`)
- Job token write limits are tested through the gate suite on main. (`pkg/gate/statewrite_test.go:804 (func TestJobTokenWritesOnlyWhatTheJobModelAllows)`)
- Runner claim behavior is tested on main. (`pkg/durable/runner_claimed_test.go:16 (func TestRunnerRunsAnOperationStartedWithItsClaim)`)
- Write-back branch naming is tested on main. (`pkg/forge/review_test.go:71 (func TestIsWriteBackBranch)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 164: Cloud sweeps on the team's runner, with the runner's read-only identity** (not owner-tagged).

- Decisions that apply: 2 (current), 5 (changed).
- Changed by: ADR 166 changes decisions 14, 15 and 19; ADR 210 changes; ADR 343 changes decision 5; ADR 343 extends decision 9. Chain to the end: ADR 166, ADR 210, ADR 343, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 166, 210, 343.
- Quote: "A sweep is not a job."
- Quote: "A sweep pool runs only sweeps."
- Effect: Jobs stay scoped to one unit and each runners job names a pool of purpose jobs; sweeps are not jobs and sweep pools stay refused at create and at claim.
- Conflict: none.

**ADR 166: Context and graph: gaps found before the implementation** (not owner-tagged).

- Decisions that apply: 19 (current).
- Changed by: ADR 210 changes; ADR 286 changes. Chain to the end: ADR 210, ADR 286, ADR 247.
- Current rule: this ADR, with ADR 210, 286.
- Quote: "The role-to-action table. Eight actions get a family and built-in roles."
- Effect: Job reads follow the same family style: namespace read then unit read, with data family actions.
- Conflict: none.

**ADR 247: Works with Backstage: an entity tab that shows the units of a component** (not owner-tagged).

- Decisions that apply: 7 (current), 8 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The API that the plugin reads (product manager). The plugin uses only the paths of ADR 210."
- Effect: Job tool-neutral routes use the dustpan prefix and the read pattern of namespace read then unit read.
- Conflict: none.

**ADR 343: Runner work: tool imports, sweeps and the runner repository** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Sweeps run on a runner (owner, 2026-11-07). ADR 164 decision 1 stays."
- Effect: Runner execution stays on runners; the server never runs the tool and this change wires only write-back execution plus domain claim ops.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the job model also cover sweeps? ADR 164 decision 2 answers it: a job belongs to one unit, a sweep changes nothing and has no unit, so this change keeps unit scope only.
- Where does tool execution run? ADR 164 decision 1 with ADR 343 decision 3 answer it: sweeps and runner work run on a runner of the team; runner claim transport stays in 130-138.
- Which runner pool may a job use? ADR 164 decision 5 answers it: a job must name a pool of the purpose jobs, checked at create and again at ClaimJob.
- Which API prefix and read pattern do job routes use? ADR 247 decisions 7 and 8 answer the prefix and read pattern: routes live under /api/dustpan/v1/ and reads check namespace read then unit read.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

Roadmap: `docs/roadmap.md:652` (A local run, two cases).

## Why

**Persona: developer.** A Developer changes one workspace from a laptop with tofu apply while two teams apply through runners. The local run must follow the same claim rules: it waits when its change overlaps a running claim and it applies side by side when disjoint, with no provider write before accept and no stale plan after a wait.

The user path this change must make work:

1. **CLI.** Run dustpan with the wrapper around tofu apply from a laptop on one workspace
2. **API.** The server registers the saved plan as a claim and refuses an overlap with the blocking claim identifier
3. **CLI.** On overlap the wrapper shows progress and tries the claim again until the blocking claim ends or the timeout ends
4. **CLI.** After the block ends the wrapper discards the old plan, makes a new plan on the new state version, then applies
5. **Docs page.** Read the wrapper guide for wait, progress, and timeout behavior
6. **E2e.** Run both local cases against real OpenTofu with a provider time log

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Claim refused because it overlaps running claim clm-9f2a | The server answers 409 with the blocking claim identifier and a retry hint. The wrapper shows the identifier, waits, and tries the claim again. | `dustpan tofu apply` |
| Timeout ended before blocking claim clm-9f2a ended | The wrapper exits with an error and makes no provider call. Run the command again after the blocker ends or raise the timeout flag. | `dustpan tofu apply --claim-timeout 60m` |
| Claim refused: caller lacks write on workspace | The server answers 403 with the missing action and the workspace object. The wrapper exits at once with no retry and no provider call. Sign in with a principal that holds the grant. | `dustpan auth status` |

Evidence for the claims above:

- The exit test defines two wrapped local cases on one workspace. Proof: A person runs `dustpan` with the wrapper around `tofu apply` from a laptop.
- An overlapping local run retries until the blocker ends or the timeout ends. Proof: part: A person runs `dustpan` with the wrapper around `tofu apply` from a laptop. - The change overlaps a running claim: the server refuses the claim, and the wrapper tries it again until the blocking claim ends, or until a timeout.
- The claim record of the local run shows ordering against the blocker. Proof: part: The claim record of the local run shows a start time after the end time of the blocking claim.
- The pack names State version e2e files for the workspace path. Proof: e2e/coverage_test.go
- The pack names the fence e2e file for the lock path. Proof: e2e/fence_test.go

## What Changes

- The wrapper reuses the claim register, base-state serve, and per-instance merge of two-disjoint-claims and concurrent-applies-merge-core.
- The wrapper waits on an overlap refusal with backoff with jitter, honors the retry hint and 503 Retry-After, and tries the claim again until the block ends or the timeout ends.
- After the block ends the wrapper discards the old plan and makes a new plan on the new state version before it applies, and re-enters wait when the new attempt meets another blocker.
- A disjoint local claim applies at the same time as a running claim; the history link of each state version stays with concurrent-applies-merge-core.
- Denied authorization exits at once with no retry; expired claim and fence refusal stop the apply; claim expiry and release timers stay with two-disjoint-claims.

Out of scope:

- local-run-without-wrapper: a plain run without the wrapper takes the whole-unit lock, owned by its own step
- strict-case-takes-turns as its own step
- runner-only-feature-in-local
- Pulumi CLI claim model
- Retry values, timeout value, claim lifetime, fence shape, and serial rule, which the RFC decides
- Plan splitting with -target
- Claim history link, expiry and release, authorization, and fence checks, owned by two-disjoint-claims and concurrent-applies-merge-core

Depends on:

- two-disjoint-claims
- runner-job-overlapping-claim-refused
- concurrent-applies-merge-core
- concurrent-applies-rfc
- `two-disjoint-claims` (order of the milestone review)
- `runner-job-overlapping-claim-refused` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A person cannot track a long wait unless the output shows the blocking claim identifier, the retry count, and the remaining timeout on each retry.
- A person who sets a short timeout by mistake loses the run unless the error names the timeout flag and the effective value.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The pack names State version e2e files and no local wrapper run file. (`e2e/coverage_test.go:1 (func none)`)
- The pack names the fence e2e file for the lock path and no claim register route. (`e2e/fence_test.go:1 (func none)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 26: OpenTofu state** (not owner-tagged).

- Decisions that apply: 3 (changed).
- Changed by: ADR 31 changes; ADR 44 changes decision 3; ADR 44 extends decision 6; ADR 45 changes decisions 4 and 5; ADR 220 changes decision 2. Chain to the end: ADR 31, ADR 44, ADR 45, ADR 220, ADR 35, ADR 221, ADR 281, ADR 239, ADR 335, ADR 62, ADR 273, ADR 286, ADR 363, ADR 272, ADR 340, ADR 364.
- Current rule: this ADR, with ADR 31, 44, 45, 220.
- Quote: "It needs the same lineage as the current state."
- Quote: "It needs a higher serial than the current state."
- Effect: This change keeps the lineage check and the serial check. Server-assigned serial under a claim and the claim lifetime stay with the RFC and with two-disjoint-claims.
- Conflict: none.

**ADR 302: Milestone 8: concurrent applies on one state** (owner-tagged (all decisions)).

- Decisions that apply: 2 (current), 4 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "A runner job registers its saved plan as a claim before the apply."
- Quote: "The `dustpan` CLI wraps the real `tofu` or `pulumi` CLI on the machine of the user, in the same way as the runner wrapper does."
- Quote: "The retry interval and the timeout of the local wrapper."
- Quote: "The code of milestone 8 comes after the project owner accepts that RFC as an ADR."
- Effect: This change specifies the wrapper wait, retry, timeout enforcement, and replan for the two local cases on the claim path. Disjointness rules, history link, expiry, fence shape, serial rule, and retry values stay with two-disjoint-claims, concurrent-applies-merge-core, and the RFC. No code lands before the owner accepts the RFC as an ADR.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does a refused local run wait or end as blocked? ADR 302 decision 2: the local wrapper waits and tries the claim again until the blocking claim ends or until the timeout ends. A runner job turns the same refusal into the status blocked.
- Does this change set the retry interval, the timeout, or the claim lifetime? No. ADR 302 decision 6 gives the retry interval and the timeout to the RFC. This change sets no number.
- Does code start before the RFC is accepted? No. ADR 302 decision 6: code comes after the owner accepts the RFC as an ADR. Build tasks wait for that ADR.
- Does this change repeat history, expiry, or auth rules owned by other changes? No. Those belong to two-disjoint-claims and concurrent-applies-merge-core.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

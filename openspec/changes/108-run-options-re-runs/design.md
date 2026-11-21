## Context

A developer wants to start a run with clear options, run it again the same way, and pass small non-secret variables without editing source. There is no run object yet, so there is nothing to set, repeat, or pass.

This change waits on the run model of milestone 5. It changes no server code, no route, and no schema now. Task 1.2 adds one error code line to the catalog when the refinement lands.

## Decisions

1. **No code now.** Options, re-runs, and variables attach to the run model when it ships. Naming them before the run object exists would fix names the run model may refuse.
2. **A closed allowlist of run options.** The refinement fixes the exact option names. An open map of names and values would surprise tool CLIs and would make validation and docs inexact.
3. **Plain-text variables with name, value, and count limits, and no secret values.** A state read masks secret values, and variables stay log-safe plain text only (ADR 41, decision 2). Secret values stay in profiles, never in variables.
4. **A re-run names its source run by stable ID and refuses a deleted unit.** Units stay in their namespace, and a soft-deleted unit restores through its own path (ADR 47). The server answers an unknown run with the NEW error code `run.no-such-run` and 404.
5. **Runs stay durable operations.** Options, re-run links, and variables attach to that model with a lease and a fence check on each write (ADR 72, decisions 2 and 4).

## Structure

No new structure now. The bug class is an option name or variable shape that the run model cannot carry; waiting for the run model plus a closed allowlist is the structure that removes it. When the run model ships, run options are an unexported record built only through its constructor, which checks the allowlist and the variable limits.

No custom analyzer and no new tool. A later refinement names the option set, the variable limits, and the constructor.

## Risks

- Developers cannot set options, re-run, or pass variables until milestone 5 ships. The docs page says so and names no workaround, since no work starts without the run model.
- A variable that looks like a secret leaks through logs and history. The constructor refuses secret-shaped values, masking applies on reads, and reveal stays a separate call.
- A re-run of a deleted unit surprises the caller. The refusal names the unit state and the one restore path.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Refuse variable values that carry secrets; check name, value, and count limits before any write. | Pass a secret value as a variable; the server refuses it and stores nothing. |
| domain | A re-run resolves its source run by stable ID and refuses a deleted or out-of-namespace unit. | Re-run a deleted unit by its old ID; the answer is `run.no-such-run`. |
| store | Carry the fence and the maintenance check on each run write. | Write run options without a fence; the store refuses it. |
| handler | Answer an unknown run ID with 404 and `run.no-such-run`; never echo a variable value into an error. | Ask for a run that does not exist; the answer carries the code and no value. |

Roadmap: `docs/roadmap.md:668` (A runner-only feature in a local run).

## Why

**Persona: developer.** A developer runs dustpan with the wrapper around tofu apply from a laptop and wants the same secret masking as a runner job, with no plain secret in the stored log.

The user path this change must make work:

1. **CLI.** Run `dustpan tofu apply` from the laptop on a workspace whose profile holds a secret.
2. **CLI.** The wrapper opens the profile, builds the mask set, and runs real `tofu` with the masker on stdout and stderr.
3. **Docs page.** Read the new wrapper guide for masking and its limits.
4. **E2e.** Run the e2e step with real `tofu` and the fake cloud provider that checks display and the log bytes the wrapper hands to the upload.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The profile open or mask set build fails before the local run starts | The wrapper starts no child and makes no provider call. Run the command again after the profile open works. | `dustpan tofu apply` |
| The log upload of the local run fails after the child exits | The stored log may be partial. Run the apply again when needed. | `dustpan tofu apply` |

Evidence for the claims above:

- Secret masking lives in pkg/launcher as StreamMasker. Proof: pkg/launcher/mask.go:42:// StreamMasker is the Masker of the launcher: it hides each secret value in
- The masker reports a closed write. Proof: pkg/launcher/mask.go:33:	ErrMaskerClosed = errors.New("the log masker is closed")
- The launcher has e2e coverage. Proof: e2e/launcher_test.go
- Masked secret values have e2e coverage. Proof: e2e/e2e_masked_test.go
- The masker has a unit test on main. Proof: pkg/launcher/mask_test.go:41 TestMaskerHidesSecrets

## What Changes

- The local wrapper builds the launcher mask set from the profile open and masks child output on display and in upload bytes.
- The wrapper drops empty secret values, refuses the start on a failed open or failed set build, and proceeds on an empty secret list.
- The wrapper flushes the masker on child exit and on a signal and never writes credential material to display or upload bytes.
- This change adds no route, no token and no authorization action. E2e proves masked display and masked upload bytes with no plain text.

Out of scope:

- Claim register, retry interval, and timeout of the local wrapper
- Job token mint, job principal, and token lifetime
- Merge of a state upload one resource instance at a time
- Port to the Pulumi CLI
- Split of a plan with -target
- Masking of plan files, state files, crash dumps, and shell history on the laptop

Depends on:

- ADR 368 on main (pull request #3052)
- ADR 369 on main (pull request #3056)
- local-run-two-cases
- `local-run-two-cases` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- The developer finds the masked value in the log without reading runner docs.
- The guide states that files and shell history on the laptop are not masked.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- StreamMasker in pkg/launcher hides each secret value. (`pkg/launcher/mask.go:42`)
- The masker reports a write after Close. (`pkg/launcher/mask.go:33`)
- A masker test exists on main. (`pkg/launcher/mask_test.go:41`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 369: The actor of a job: trust rules, conditions on token claims, and external service principals** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The local wrapper gets a launcher token that is bound to its apply claim."
- Quote: "The local wrapper always acts as the person who signed in."
- Effect: The local step authenticates with the launcher token bound to the apply claim, and the actor is the signed-in person.
- Conflict: none.

**ADR 302: Milestone 8: concurrent applies on one state** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current), 5 (current), 6 (current).
- Changed by: ADR 368 extends decision 6; ADR 369 extends decision 6. Chain to the end: ADR 368, ADR 369.
- Current rule: this ADR, with ADR 368, 369.
- Quote: "A local CLI wrapper (owner). The `dustpan` CLI wraps the real `tofu` or `pulumi` CLI on the machine of the user, in the same way as the runner wrapper does. A local run can then take part: it registers claims, and it can use the other features that today work only in OpenDustpan runners."
- Quote: "Milestone 8 ports each of these features to the local wrapper."
- Effect: The change ports only secret masking with StreamMasker. It adds no claim, token or route. ADR 369 decides the credential of the local step.
- Conflict: none.

## Open questions

none

Answered by ADR 369 (owner, 2026-11-14): the local step authenticates with a launcher token bound to the apply claim, and the actor is the person who signed in. This restores the token decision that the owner approved on 14 November 2026 at about 13:15 UTC.

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does the C1 inventory name a runner-only feature beyond secret masking that this change must also port?
   - Options: Secret masking only in this change; Secret masking plus each non-exempt C1 entry in this change.
   - Provisional decision (owner review 14 Nov): Secret masking only in this change
   - Reason: The pack names no C1 inventory, so no other entry is proven. Benchmark: Google: one reviewable change per concern, each with its own acceptance check.
   - Persona impact: A developer gets masking now and other features later with no surprise. Escape hatch: Each later C1 entry ships in its own change.
   - Owner-tagged ADRs involved: ADR 302 (their words do not change).

Questions considered and answered:

- Does the local wrapper need secret masking? Yes. ADR 302 decisions 4 and 5 require the port, and the step names Masker in pkg/launcher.
- Does this change add a server route or mint a token? No. The claim, the job principal and the token are RFC scope per ADR 302 decision 6. This change only masks bytes.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

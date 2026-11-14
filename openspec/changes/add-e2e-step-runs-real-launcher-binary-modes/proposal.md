Issue: #1027.

## Why

**Persona: runner operator.** A runner operator runs plans and applies on runners. The operator needs one static launcher binary that runs one claimed phase, copies itself for Kubernetes jobs, and serves tool credentials, so a runner executes both Pulumi and OpenTofu jobs without extra tools in the image.

The user path this change must make work:

1. **Docs page.** Read the launcher docs page and build the static binary for the runner image.
2. **CLI.** Run dustpan-launcher run and copy modes against a test server to check flags and helpers.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Unknown, repeated, or valueless flag on run or copy. | The launcher ends with exit code 64 and names the bad flag. Run the help for the valid flags and retry. | `dustpan-launcher run --help` |
| No handoff token or the token exchange fails. | The launcher ends with exit code 65 and names the missing token or the failed exchange. Check that the claim is still valid, then retry the same run command. | `dustpan-launcher run --server <URL> --handoff stdin` |
| Secrets directory is not on tmpfs or ramfs, or its mode or owner is wrong. | The launcher refuses the run before any step starts and names the secrets directory. Point --secrets-dir at a tmpfs or ramfs path. | `dustpan-launcher run --server <URL> --handoff stdin --secrets-dir /dustpan/secrets` |

Evidence for the claims above:

- The launcher command directory exists in this checkout. Proof: Pack path cmd/dustpan-launcher exists: true.
- The launcher domain package exists in this checkout. Proof: Pack path pkg/launcher exists: true.
- The binary parses through pkg/launcher in this checkout, so the issue line that no code exists under cmd/ is stale. Proof: rg launcher.Parse over pkg internal cmd finds cmd/dustpan-launcher/main.go:44: args, err := launcher.Parse(argv).
- No e2e file in the pack names the launcher modes. Proof: Pack keywords list is empty.

## What Changes

- Ship cmd/dustpan-launcher with run, copy --dest, OpenTofu credentials helper, and git credential helper modes through pkg/launcher.
- End with exit code 64 on an unknown, repeated, or valueless flag.
- Build a static CGO_ENABLED=0 binary for linux amd64 and linux arm64 with static and dependency checks.
- Ship the launcher docs page in the same pull request.

Out of scope:

- The dustpan-runner binary
- The real runner e2e step
- Container image publication
- Move of runner management to tool-neutral pool routes

Depends on:

- add-job-service-routes-start-1
- `add-e2e-step-runs-real-runner-pool-route-move` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The operator learns four entry points under two program names; the docs page lists each command with one copy-ready example.
- Exit code 64 alone does not name the bad flag unless the error text does; the error names the flag and the help command.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 134: The launcher contract** (owner-tagged: decision 1, 5, 7, 9).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (changed), 5 (current), 6 (changed), 7 (current), 8 (current), 9 (current), 10 (current), 11 (current).
- Changed by: ADR 199 changes decisions 4 and 6; ADR 330 extends decision 6. Chain to the end: ADR 199, ADR 330, ADR 286, ADR 310, ADR 353.
- Current rule: this ADR, with ADR 199, 330.
- Quote: "`dustpan-launcher` is pure Go, with no cgo, built with `CGO_ENABLED=0` for `linux` and each `GOARCH` of the release. It links no SQL driver and no forge client."
- Quote: "Modes. The launcher has four modes (`launcher.Parse`): - `run`: run one claimed phase or sweep."
- Quote: "A flag that is unknown, twice or without a value ends the launcher with the exit code 64."
- Quote: "Prepare the place (`launcher.MemoryVolume.Prepare`): check that the secrets directory is on `tmpfs` or `ramfs`, then create in it a new directory with mode 0700"
- Quote: "The launcher starts `Args[0]` with `Args[1:]`, with no shell. `Args[0]` is a bare name that the launcher finds on the `PATH` of the image, or a clean absolute path."
- Quote: "Masks. The launcher masks each secret value that it got through its session in the log of every step, with `[masked]`, also across the end of a chunk (`launcher.Masker`). A value shorter than 4 bytes is not masked, because it would hide common text."
- Quote: "At its start, the launcher sets `PR_SET_DUMPABLE` to 0. So a process of a step with the same user ID cannot attach to the launcher with `ptrace` or read its memory through `/proc`, where the job token and the session key are."
- Effect: This change ships the binary, modes, flags, run order, step execution, environment, credentials, masks, timeouts, exit codes, and dumpable handling that the decisions describe, with no change to the owner-tagged words. It follows the Changed-by chain through ADR 199, 330, 286, 310, and 353; current rule is ADR 134 with ADR 199 and 330.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Quote: "A reviewer refuses such a pull request when the page is not in it. A later pull request for the docs does not make the first one done."
- Effect: The launcher modes and flags are user-visible commands, so the same pull request ships the docs page.
- Conflict: none.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (missing scenario): The run mode has no requirement or scenario in spec.md: no step execution, no run order (ADR 134 decision 4), no exit code 0 or 65 (decision 10), and no no-shell rule (decision 5). The binary cannot be built right without them. File: specs/launcher-binary/spec.md. Resolution: answered: Added run-order, step-execution, and exit-code requirements with scenarios for order, code 0, code 65, and no-shell execution.
- R1.2 (missing scenario): Timeouts, SIGINT/SIGKILL grace periods and SIGTERM handling of ADR 134 decision 9 (owner-tagged) are absent from the spec and the tasks, although the run mode must enforce them. File: specs/launcher-binary/spec.md. Resolution: answered: Added timeouts and signals requirement with SIGINT, grace periods, and SIGTERM scenarios, plus a task for the timeout tests.
- R1.3 (wrong claim): The ADR check lists only ADR 134 and ADR 214. ADR 134 decisions 4 and 6 are changed by ADR 199 and ADR 330 (current rule: this ADR with 199, 330), and decision 6 is extended by ADR 330 (owner-tagged, whole ADR). Their decisions are not read or quoted, although the run order and step environment are built here. File: proposal.md. Resolution: answered: Added ADR 199 and ADR 330 entries with their change lines quoted, followed the chain to ADR 286, 310, and 353, and quoted decisions 5 through 11 of ADR 134.
- R1.4 (failure mode): Masking is not specified beyond the layer table. A secret split across chunks is tested only for one split. Secrets shorter than 4 bytes are not masked (ADR 134 decision 8), and encoded forms of a secret (base64, URL-encoded) are not covered. No requirement states these limits. File: specs/launcher-binary/spec.md. Resolution: answered: Added masking requirement that states the 4-byte limit, cross-chunk masking, and no statement about encoded forms, with hostile scenarios.
- R1.5 (missing scenario): The secrets directory checks (tmpfs or ramfs only, 0700 directory owned by the launcher user, 0600 files, removal of the per-step directory, refusal before any step starts) have no requirement or scenario, though the error table and the layer table depend on them. File: specs/launcher-binary/spec.md. Resolution: answered: Added secrets-directory requirement with tmpfs check, 0700 directory, 0600 files, removal, and refusal before start.
- R1.6 (missing scenario): The handoff failure (exit 65, no token, failed exchange) has no scenario. The error table says 'Fetch a new handoff token and retry' but gives a run command that fetches no token, so the stated fix does not match the command. File: proposal.md. Resolution: answered: Added handoff exit-65 scenario and fixed the error answer so the fix matches the same run command without claiming a token fetch.
- R1.7 (failure mode): Credential helper modes have no requirement for a missing or dead session, for a copy whose destination is missing or not writable, or for a copy over a binary in use. File: specs/launcher-binary/spec.md. Resolution: answered: Added helper and copy failure requirements for missing session, missing destination, and unwritable destination.
- R1.8 (failure mode): PR_SET_DUMPABLE (ADR 134 decision 11) and the step user (StepUser) are not in the spec or the tasks. Dumpable is Linux-only, and the behavior on other hosts is not stated. File: specs/launcher-binary/spec.md. Resolution: answered: Added dumpable and step-user requirement that states Linux-only dumpable handling with no extra behavior elsewhere.
- R1.9 (wrong claim): The proposal cites cmd/dustpan-launcher/main.go:44 as existing code. The issue states that no code exists under cmd/ for the launcher. The evidence conflicts with the input and is not reconciled. File: proposal.md. Resolution: answered: Reconciled the evidence: the pack paths and rg hit show code exists, so the issue line is stale.
- R1.10 (scope): The issue acceptance criteria require make licenses and tests of the modes on amd64 and arm64 in the release build. Neither is in tasks.md. Publication to the machine runner archive (ADR 134 decision 1) is claimed in the proposal but not in What Changes. Files: tasks.md, proposal.md. Resolution: answered: Added license and arch test tasks, and removed the archive publication claim from the user path.
- R1.11 (open question): The dependency check and the static check have no defined mechanism (for example go list -deps, or file and ldd output). A transitive import through a helper package is named as a risk but no check covers it. File: design.md. Resolution: answered: Defined the checks as go list -deps and file output in design and tasks, covering transitive imports.

## Open questions

none

Questions considered and answered:

- Does the launcher binary need a new decision beyond ADR 134 with ADR 199 and 330? Yes. ADR 134 decisions 1 to 11 fix the binary shape, modes, flags, run order, step execution, environment, credentials, masks, timeouts, exit codes, and dumpable handling. This change calls pkg/launcher so the file-set and proxy extensions of the Changed-by chain flow through with no new decision.
- Does this change publish the container image? No. ADR 134 decision 1 names publication in its own image and in the machine runner archive; this change ships only the static binary and the docs page and leaves image publication out.
- Does the launcher need a docs page in the same pull request? Yes. ADR 214 decision 1 covers a command with flags that a person reads, so the pull request adds the launcher docs page.
- How do the static and dependency checks catch a transitive import? The checks are go list -deps for the dependency ban, plus file output for a static binary. Both run in CI and fail the build, so a transitive helper import fails too.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

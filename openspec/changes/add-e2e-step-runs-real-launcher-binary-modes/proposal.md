---
version: 2
---

Issue: #1027.

## Why

**Persona: runner operator.** A runner operator runs plans and applies on runners. The operator needs one static launcher binary that runs one claimed phase, copies itself for Kubernetes jobs, and serves tool credentials, so a runner executes both Pulumi and OpenTofu jobs without extra tools in the image.

The user path this change must make work:

1. **Docs page.** Read the launcher docs page and build the static binary for the runner image.
2. **CLI.** Run dustpan-launcher run and copy modes to check flags and helpers.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Unknown, repeated, or valueless flag on run or copy. | The launcher ends with exit code 64 and names the bad flag. | `dustpan-launcher run --help` |
| No handoff token or the token exchange fails. | The launcher ends with exit code 65 and starts no step. | `dustpan-launcher run --server <URL> --handoff stdin` |
| Secrets directory is not on tmpfs or ramfs, or its mode or owner is wrong. | The launcher refuses the run before any step starts and names the secrets directory. | `dustpan-launcher run --server <URL> --handoff stdin` |

Evidence for the claims above:

- The launcher command directory exists in this checkout. Proof: Pack path cmd/dustpan-launcher exists: true.
- The launcher domain package exists. Proof: Pack path pkg/launcher exists: true.
- The entry point already calls the parser, so this change completes the modes, checks, build and docs around it. Proof: rg launcher.Parse over pkg internal cmd finds cmd/dustpan-launcher/main.go:44.
- No keyword evidence names the launcher modes. Proof: Pack keywords list is empty.

## What Changes

- Ship cmd/dustpan-launcher with run, copy --dest, OpenTofu credentials helper and git credential helper modes through pkg/launcher.
- End with exit code 64 on an unknown, repeated or valueless flag.
- Build a static CGO_ENABLED=0 binary for linux amd64 and linux arm64 with static and dependency checks.
- Ship the launcher docs page in the same pull request.

Out of scope:

- The dustpan-runner binary
- The real runner e2e step, in the sibling part 3 change
- Container image publication
- Release archive publication for the machine runner
- Move of runner management to tool-neutral pool routes

Depends on:

- add-job-service-routes-start-1
- `add-e2e-step-runs-real-runner-pool-route-move` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The operator learns four entry points under two program names; the docs page lists each with one example.
- Exit code 64 alone does not name the bad flag; the error text names it.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The parser lives in pkg/launcher. (`pkg/launcher/launcher.go:144`)
- The launcher main calls the parser. (`cmd/dustpan-launcher/main.go:44`)
- Step stop behavior has a test. (`pkg/launcher/run/run_test.go:182`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 134: The launcher contract** (owner-tagged: decision 1, 5, 7, 9).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 4 (changed), 5 (current), 6 (changed), 7 (current), 8 (current), 9 (current), 10 (current), 11 (current).
- Changed by: ADR 199 changes decisions 4 and 6; ADR 330 extends decision 6. Chain to the end: ADR 199, ADR 330, ADR 286, ADR 310, ADR 353.
- Current rule: this ADR, with ADR 199, 330.
- Quote: "`dustpan-launcher` is pure Go, with no cgo, built with `CGO_ENABLED=0` for `linux` and each `GOARCH` of the release. It links no SQL driver and no forge client."
- Quote: "A flag that is unknown, twice or without a value ends the launcher with the exit code 64."
- Effect: The change ships the binary, modes, flags and run order that the decisions describe, with no change to the owner-tagged words.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The launcher modes and flags are user-visible, so the same pull request ships the docs page.
- Conflict: none.

## Open questions

Not spec-ready: a code check fails: a security change needs "threats.dfd": a Mermaid flowchart with each trust boundary as a subgraph; a code check fails: contract method Parse: no requirement or scenario names it.

Questions considered and answered:

- Does the launcher binary need a new decision beyond ADR 134 with ADR 199 and 330? No. ADR 134 decisions 1 to 11 fix the binary. The change calls pkg/launcher, so later extensions of decisions 4 and 6 flow through unchanged.
- Does this change publish the image or the machine runner archive? No. ADR 134 decision 1 names them, but this change ships only the binary and docs page.
- Does the launcher need a docs page in the same pull request? Yes. ADR 214 decision 1 covers a command with flags that a person reads.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

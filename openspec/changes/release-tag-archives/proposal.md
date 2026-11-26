---
version: 2
---

Issue: #3662

## Why

**Persona: operator, and the maintainer who publishes the release.** The scripts that build and check the binary archives are on main. The tag workflow does not use them. A `v*` tag builds and pushes the container image and the Helm chart, and nothing else. After the first tag, an operator who wants a binary archive finds none, and a maintainer must build, check and upload the archives by hand, with no record of who did it.

The user path this change must make work:

1. **Maintainer, tag.** A person with release permission pushes the tag `v0.1.0`. This is the only act that starts the workflow.
2. **Workflow.** The workflow builds the four archives and `SHA256SUMS`, checks them in a second job from the stored files, pushes the image and the chart, and last creates a draft release that holds the archives and `SHA256SUMS`.
3. **Maintainer, publish.** The maintainer opens the draft, reads the file list and the checksums, and publishes the draft. No job of the workflow publishes it.
4. **Operator.** The operator downloads one archive and `SHA256SUMS` from the release page, checks the checksum and installs, as the install page says.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The tag is not `v` and three dot separated numbers, or the release record has no line for it. | The build job stops with the script message before any file exists. | `make release-record-check VERSION=v0.1.0` |
| An archive or `SHA256SUMS` is wrong after the build. | The verify job stops. The image job, the chart push and the upload do not run. | `make release-verify VERSION=v0.1.0` |
| A release for the tag exists already, as a draft or as a published release. | The upload step refuses and changes nothing. | `gh release list --repo OpenDustpan/OpenDustpan` |
| The upload stops after the draft exists, with some files. | The draft stays a draft. The step says to delete it and to re-run the failed job. | `gh release delete v0.1.0 --repo OpenDustpan/OpenDustpan` |

## What Changes

The upload script, its tests, the maintainer section of the install page and the Makefile line are on main (pull request 3762). This change is the rest: the tag workflow and the test of its structure. It is one pull request, and the managed Droid computer `dustpan` opens it, because the cloud agent cannot push `.github/workflows`.

- The tag workflow gets three changes (a workflow patch, see "Workflow patch" in the design): the jobs `archives-build` and `archives-verify` before the image job, the job `release-draft` after it, and a `permissions` block for each job in place of the block for the whole file.
- A test of the structure of the workflow. A checker function reads the workflow file (the design says which of the two paths) and returns one finding for each missing guard. A second test runs the checker on mutated copies of the file. The tests run in the Go job of every pull request, so no tag is needed.

Out of scope:

- Tagging or releasing. The workflow runs on a tag push only. No one tags until the owner says so.
- The archive build and verify scripts and the upload script. They are on main and unchanged.
- The Helm chart. The workflow pushes the chart already. The release page does not list the chart: the chart is an OCI artifact in the registry. See "Decisions" in the design.
- Signing, an SBOM, OS packages and a `zip` archive. ADR 283 decision 4 and ADR 278 exclude them.
- The operator part of the install page (the download, check and unpack commands). It is on main, and it still says that no archive exists. The first real release is the time to change that sentence.

Depends on, all on main:

- `release-artifacts-binary-archives`: the build and verify scripts.
- The upload script of pull request 3762.

## Prototype output

The workflow test reads `.github/workflows/release.yml`. This is the output of a prototype run on main (head `ddf630a81`): it parses the file and lists each job with its `needs`, its `permissions`, each `uses:` pin and each `run:` block that holds a template value. The counts in this change come from it.

```
permissions(file): {'contents': 'read', 'packages': 'write'}
publish needs= None permissions= None if= github.event_name == 'push' &&...
   uses actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
   uses docker/setup-qemu-action@99012661954931238ded8c8b007157a8430204e1
   uses docker/setup-buildx-action@f87e5991a6d7451dcb8d9637bfbc97413f497069
   run blocks with a template value: none
```

So main has one job, no `needs`, no job-level `permissions`, three pins (all full SHAs) and no `run:` block with a template value. The same prototype on the patched file prints: file permissions `{}`, four jobs (`archives-build`, `archives-verify`, `publish`, `release-draft`), `needs` on three of them, a `permissions` block on each, ten pins (checkout in four jobs, `setup-go`, `upload-artifact`, `download-artifact` twice, and the three of `publish`) and no `run:` block with a template value.

## Usability

Where the path still costs the person time or surprises them:

- The release page has no files until a person publishes the draft. The maintainer section of the install page says so, and it says what to read in the draft first.
- A failed run can leave a draft with some files. The step message says to delete the draft and re-run. Use "Re-run failed jobs", not "Re-run all jobs": the chart step refuses a second push of a released tag, so a full re-run stops there, before the draft job.
- `SHA256SUMS` is no signature (ADR 283 decision 4). The maintainer section and the release notes say so. They make no compliance claim.

## What main already does

Each fact names its file, line and function on main at ddf630a81. The change specifies only the difference.

- The upload script ends with a one-line refusal on stderr and a non-zero exit code. (`scripts/release-upload.sh:36 (func refuse)`)
- The verify script reports each refusal with one function. (`scripts/release-verify.sh:40 (func fail)`)
- The make target `release-build` builds the four archives with the version in the environment. (`Makefile:209 (make target release-build)`)
- The make target `release-record-check` passes the version through the environment. (`Makefile:194 (make target release-record-check)`)
- The test of the build builds the archives twice in a copy of the work tree. (`internal/releaserecord/release_archives_test.go:32 (func TestReleaseArchivesAreReproducible)`)
- The test of the upload script runs it with a fake `gh` and reads the recorded calls. (`internal/releaserecord/release_upload_test.go:325 (func TestReleaseUploadCreatesOneDraft)`)
- The test of the build refuses a bad version. (`internal/releaserecord/release_archives_test.go:176 (func TestReleaseBuildRefusesABadVersion)`)
- The test of the verify script refuses bad input. (`internal/releaserecord/release_verify_test.go:324 (func TestReleaseVerifyRefusesBadInput)`)
- The existing workflow test reads the file as text. (`e2e/release_workflow_test.go:57 (func TestReleasePushesOneDigestToBothRegistries)`)
- The existing test helper reads the last job-level `if:` line, so the guard line of each new job keeps the same text. (`e2e/release_workflow_test.go:202 (func jobGuardClauses)`)

Facts about files that these markers do not cover, from main at ddf630a81:

- The workflow runs on a push of a tag `v*`, and it sets `contents: read` and `packages: write` for the whole file, at `.github/workflows/release.yml` lines 23 to 29.
- The workflow has one job, `publish`, with the guard `github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v') && github.repository == 'OpenDustpan/OpenDustpan'` at line 39. The job runs on `ubuntu-26.04` and has no `needs` and no `permissions` block.
- The workflow has no archive step, no call of a release script and no `gh release` call: `rg -n "release-build|release-verify|release-upload|gh release|upload-artifact" .github/workflows/release.yml` prints nothing.
- The job `publish` checks the actor (line 46) and checks that the tag is `vX.Y.Z` (line 61). It builds the image once with `--build-arg VERSION="${TAG}"` (line 161) and pushes the chart, and refuses a chart version that is released (line 213).
- Actions are pinned by full SHA with the release in a comment, for example `actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1` (line 42).
- Other workflows pin `actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0` (`.github/workflows/ci.yml` line 178), `actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2` (`.github/workflows/auto-regen-resolve.yml` line 97) and `actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2` (`.github/workflows/auto-regen-push.yml` line 69).
- The install page has the section "Maintainer: publish the release archives" at line 224 of `docs/guides/install.md`.
- `go.yaml.in/yaml/v3` is a direct requirement at line 15 of `go.mod`, and a test already imports it, so the workflow test adds no dependency.

## ADR check

Each quote is copied from the file in `docs/adr/`. The owner tags, the "Changed by" chain and the "Current rule" are from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)".

**ADR 283: Packaging and installation for v0.1.0** (owner-tagged: decisions 1 to 5 carry "(owner)"; decisions 6 and 7 are not tagged).

- Decisions that apply: 3, 4, 5, 6.
- Changed by: ADR 315 extends. Current rule: this ADR, with ADR 315.
- Quote, decision 3: "`v0.1.0` ships binary archives, the container image and the Helm chart. It ships no `.deb`, `.rpm` or other OS package."
- Quote, decision 4: "`v0.1.0` ships the `SHA256SUMS` file of ADR 27 decision 6. It ships no signature of the archives or of the image, and no SBOM. A later ADR decides them."
- Quote, decision 5: "Each `uses:` line pins the action by its full commit SHA, with the version tag in a comment." and "The pinned commit is at least two weeks old when the workflow lands."
- Quote, decision 6: "A `v*` tag starts the workflow of the binaries and the image."
- Effect: the change adds the archives and `SHA256SUMS` to the tag workflow, as decisions 3, 4 and 6 say. It adds no signature and no SBOM. The pins follow decision 5. The three actions that it adds are already pinned on main, and each pin is more than two weeks old. The chart is already in the workflow, so decision 3 holds for the chart with no change.
- Conflict: none.

**ADR 27: TLS and release artifacts** (not owner-tagged; the owner delegated it to an agent session and can supersede it).

- Decisions that apply: 6.
- Changed by: ADR 212 changes decision 2; ADR 246 extends decision 8; ADR 278 changes decision 6; ADR 283 changes decision 8; ADR 283 extends decision 7. Current rule: this ADR, with ADR 212, 246, 278, 283.
- Quote, decision 6: "A workflow that a tag starts builds the binaries with a `go build` matrix. It makes `tar.gz` and `zip` archives and a `SHA256SUMS` file. The project does not use GoReleaser."
- Quote, ADR 278 change to decision 6: "only Linux and macOS archives, no zip archive."
- Effect: the build runs `scripts/release-build.sh`, which runs `go build` for the four pairs in one script and not in a job matrix. The decision names the result (archives and `SHA256SUMS`) and the rule that the project uses no release tool. The change keeps both and adds no release tool: it uses `gh`, which the runner image has. The script that does the build was accepted with the change `release-artifacts-binary-archives` and is on main. The change makes no zip archive (ADR 278).
- Conflict: none.

**ADR 278: Supported platforms are Linux and macOS** (owner-tagged).

- Decisions that apply: 1, 2.
- Quote, decision 1: "The supported platforms are Linux and macOS, on `amd64` and `arm64`."
- Quote, decision 2: "Windows and all other platforms are not supported. The project does not promise that the binary works there. The release build makes no archive for them."
- Effect: the four pairs of the build script are the four pairs of the release. The change adds no other platform and no build tag.
- Conflict: none.

**ADR 246: A Helm chart for `opendustpan serve`** (not owner-tagged as a whole; decision 5 carries "(owner)").

- Decisions that apply: 1, 2, 3.
- Quote, decision 1: "The tag workflow that builds the binaries and the image also packages the chart."
- Quote, decision 3: "The tag workflow pushes the chart as an OCI artifact to the registry of the image. It publishes no `index.yaml` on a web page."
- Effect: the chart ships through the registry, not through the release page. The change leaves the chart step as it is.
- Conflict: none.

**ADR 21: The first tagged release** (not owner-tagged).

- Quote, "Artifacts": "The release ships binary archives that are cross-built, with checksums."
- Quote, "Process": "A workflow that a tag triggers makes the release. A new release tool (for example GoReleaser) needs a "yes" from the owner first."
- Effect: the change uses the existing scripts and the `gh` command of the runner image, and adds no release tool and no dependency.
- Conflict: none.

**ADR 279: No release before the exit tests of milestones 1 to 7 pass** (not owner-tagged).

- Effect: no tag and no release come from this change. The workflow runs on a tag push only, and a tag push is the act of a person. Nothing in the change pushes a tag.
- Conflict: none.

**ADR 325: The release record turns the shipped-migration checks on** (not owner-tagged: a PM decision on the owner's behalf; it changes the mechanism of ADRs 322 and 323 only).

- Effect: The build script runs the record check first. The change keeps that: a tag whose commit has no record lines fails in the build job, before any image is pushed.
- Conflict: none.

**ADR 250: Defense in depth for security code** (owner-tagged: "The project owner decided it.").

- Decisions that apply: 1, 4.
- Quote, decision 1: "Every security-relevant path has at least two independent layers. Each layer must stop the attack alone, without help from the others."
- Quote, decision 4: "The test sends hostile input directly to that layer, as if every other layer had failed."
- Effect: the supply-chain parts of the change (the pins, the permissions, the checksum check and the upload) are security code. "Layers" in the design lists each layer and its hostile test.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Draft or published release? A draft. ADR 283 decision 3 says the release ships the archives, and it names no release type. A draft changes nothing for an operator until a person publishes it, and a person can delete it. A published release cannot be undone without a public trace. No ADR asks for an automatic publish. The change keeps the draft step as one `--draft` flag, so the owner can ask for another type later with a one-line change.
- Does the chart belong to this change? No. The workflow pushes it already, and ADR 246 decision 3 says it ships as an OCI artifact in the registry. A release page file would be a second copy with a second checksum story. See "Decisions" in the design.
- Sign the archives? No. ADR 283 decision 4.
- Which job runs first, the image or the archives? The archives. The archives are built and checked before the first push to a registry, because a registry push is public and cannot be taken back. The draft is created last, because it is the one step that a person can undo.
- Where does the dry run run? In the Go job of every pull request, as tests. No tag and no workflow run are needed. See "Test path with no tag" in the design.
- Does the change tag or release? No. See ADR 279 above.

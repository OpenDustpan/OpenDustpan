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

- A new script `scripts/release-upload.sh <version> <dir>` checks the set again with `scripts/release-verify.sh`, with a shape allowlist (the five names, exactly one line for each archive and no other line) and with `sha256sum --check --strict`, also when `release-verify.sh` is replaced by a script that passes, refuses when a release for the tag exists, and creates one draft release with the four archives and `SHA256SUMS`. It never publishes.
- The tag workflow gets three changes (a workflow patch, see "Workflow patch" in `design.md`): the jobs `archives-build` and `archives-verify` before the image job, the job `release-draft` after it, and a `permissions` block for each job in place of the block for the whole file.
- `make release-record-check` passes `VERSION` through the environment, so that a quote in the value cannot end the shell quoting.
- A test path with no tag: the tests of the new script run in the Go job of every pull request. They build the archives of a test version in a copy of the work tree and run the new script with a fake `gh`. A second test reads `release.yml` and checks its structure.
- `docs/guides/install.md` gets one maintainer section: "Maintainer: publish the release archives".

Out of scope:

- Tagging or releasing. The workflow runs on a tag push only. No one tags until the owner says so.
- The archive build and verify scripts. They are on main and unchanged.
- The Helm chart. The workflow pushes the chart already (`.github/workflows/release.yml:213-235`). The release page does not list the chart: the chart is an OCI artifact in the registry. See "Decisions" in `design.md`.
- Signing, an SBOM, OS packages and a `zip` archive. ADR 283 decision 4 and ADR 278 exclude them.
- The operator part of the install page (the download, check and unpack commands). The change `release-artifacts-image-install-docs` owns it. See "Relation to other changes" in `design.md`.
- The image `--build-arg VERSION` and the console pin. That change owns them.

Depends on:

- `release-artifacts-binary-archives`. Its scripts are on main (`scripts/release-build.sh`, `scripts/release-verify.sh`, `Makefile:198-212`).

## Prototype output

The workflow test reads `.github/workflows/release.yml`. This is the output of a prototype run on main (head `3443bb35d`): it parses the file and lists each job with its `needs`, its `permissions`, each `uses:` pin and each `run:` block that holds a template value. The counts in this change come from it.

```
permissions(file): {'contents': 'read', 'packages': 'write'}
publish needs= None permissions= None if= github.event_name == 'push' && startsWit...
   uses actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
   uses docker/setup-qemu-action@99012661954931238ded8c8b007157a8430204e1
   uses docker/setup-buildx-action@f87e5991a6d7451dcb8d9637bfbc97413f497069
(no run: block holds a template value)
```

So main has one job, no `needs`, no job-level `permissions`, three pins (all full SHAs) and no `run:` block with a template value. After the patch: four jobs, `needs` on three of them, a `permissions` block on each, eight pins in total (checkout in four jobs, `setup-go`, `upload-artifact`, `download-artifact` twice, plus the three of `publish`), and still no `run:` block with a template value.

## Usability

Where the path still costs the person time or surprises them:

- The release page has no files until a person publishes the draft. The maintainer section says so, and it says what to read in the draft first.
- A failed run can leave a draft with some files. The step message says to delete the draft and re-run. Use "Re-run failed jobs", not "Re-run all jobs": the image and chart steps refuse a second push of a released tag, so a full re-run stops there, before the draft job.
- `SHA256SUMS` is no signature (ADR 283 decision 4). The maintainer section and the release notes say so. They make no compliance claim.

## What main already does

Each fact names its file, line and function on main (head `3443bb35d`). The change specifies only the difference. These facts were checked in the session that wrote this change.

- The workflow runs on a push of a tag `v*` (`.github/workflows/release.yml:22 (the on block of workflow release)`). It has one job, `publish`, with the guard `github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v') && github.repository == 'OpenDustpan/OpenDustpan'` (`:39`). The permissions are for the whole file: `contents: read` and `packages: write` (`:27-29`). It has no `needs`, no archive step, no `scripts/release-*.sh` call and no `gh release` call (`rg -n "release-build|release-verify|gh release|upload-artifact" .github/workflows/release.yml` prints nothing).
- The job checks the actor (`:46`), checks that the tag is `vX.Y.Z` (`:61`), builds the image once with `docker buildx build ... --push .` and no `--build-arg` (`:161`), compares the digests of both registries, and pushes the chart with `helm package` and `helm push` (`:213-235`).
- Actions are pinned by full SHA with the release in a comment: `actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1` (`:42`), `docker/setup-qemu-action` (`:84`), `docker/setup-buildx-action` (`:86`). Other workflows on main pin `actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0` (`.github/workflows/ci.yml:173`), `actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2` (`.github/workflows/auto-regen-resolve.yml:97`) and `actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2` (`.github/workflows/auto-regen-push.yml:69`).
- The build script takes two arguments (`scripts/release-build.sh:13 (func main body, argument check)`) and checks the version against `^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$` (`:30`). It runs `scripts/check-release-record.sh` first (`:42`). It builds four pairs, `linux amd64`, `linux arm64`, `darwin amd64` and `darwin arm64` (`:57-62`), with `CGO_ENABLED=0 ... go build -trimpath -buildvcs=false -ldflags "-X main.version=${version}"` (`:70-72`). Each archive is `opendustpan_${version}_${os}_${arch}.tar.gz` (`:69`) with `LICENSE`, `README.md`, the binary and `third_party_licenses/` (`:73-84`). It writes `SHA256SUMS` with `sha256sum -- *.tar.gz` (`:87`), so each line is `<64 hex>  <name>`.
- The verify script takes two arguments (`scripts/release-verify.sh:111 (func main body, argument check)`) and reports each refusal through `scripts/release-verify.sh:127 (func fail)`. It refuses a set unless the directory holds exactly the four archives and `SHA256SUMS` (`:132-153`), the `SHA256SUMS` lines match `^([0-9a-f]{64})\ \ ([^[:space:]/]+)$` (`:158`), each checksum matches (`:175-178`), each archive holds only the allowed entries with `LICENSE` and a file of `third_party_licenses/` (`:184-229`), and the binary carries the version stamp (`:235-237`). It never runs the binary and never writes a file (`scripts/release-verify.sh:111`)
- `make licenses` checks the license files for the same four pairs (`Makefile:141`)
- `make release-record-check` interpolates the version into the shell text: `./scripts/check-release-record.sh '$(VERSION)'` (`Makefile:193`). A value with a single quote ends the quoting. This is the same fault as issue 3661 (`Makefile:193 (make target release-record-check)`)
- The test builds the archives twice in a copy of the work tree with an extended release record ; the helpers `copyWorkTree`, `extendRecord` and `runInTree` exist (`internal/releaserecord/release_archives_test.go:32 (func TestReleaseArchivesAreReproducible)`)
- The existing tests check the structure of `release.yml` as text (`e2e/release_workflow_test.go:57 (func TestReleasePushesOneDigestToBothRegistries)`). The tests pass on the file as the patch of this change leaves it (the session that wrote this change ran `go test -tags e2e -run TestReleasePushesOneDigestToBothRegistries ./e2e` with the patch applied, then reverted it)
- The Helm chart is released by the same workflow: it is packaged with the tag as chart version and app version and pushed as an OCI artifact to `oci://ghcr.io/opendustpan/charts` (`.github/workflows/release.yml:213-235`). `docs/guides/install.md` shows `helm install ... oci://ghcr.io/opendustpan/charts/opendustpan --version 0.1.0`.
- `docs/guides/install.md` has no section for the archives and no section for the publish of a release. Its last section is "Maintainer setup: Docker Hub secrets" (`docs/guides/install.md:73`)
- `go.yaml.in/yaml/v3` is a direct requirement in `go.mod:15` and a test already imports it (`deploy/helm/charttest/values_reference_test.go`). The workflow test can parse YAML with it and adds no dependency.

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
- Quote, "Process": "A workflow that a tag triggers makes the release. A new release tool (for example GoReleaser) needs a "yes" from the owner first (`CLAUDE.md`: ask before adding a dependency)."
- Effect: the change uses the existing scripts and the `gh` command of the runner image, and adds no release tool and no dependency.
- Conflict: none.

**ADR 279: No release before the exit tests of milestones 1 to 7 pass** (not owner-tagged).

- Effect: no tag and no release come from this change. The workflow runs on a tag push only, and a tag push is the act of a person. Nothing in the change pushes a tag.
- Conflict: none.

**ADR 325: The release record turns the shipped-migration checks on** (not owner-tagged: a PM decision on the owner's behalf; it changes the mechanism of ADRs 322 and 323 only).

- Effect: `scripts/release-build.sh` runs the record check first. The change keeps that: a tag whose commit has no record lines fails in the build job, before any image is pushed.
- Conflict: none.

**ADR 250: Defense in depth for security code** (owner-tagged: "The project owner decided it.").

- Decisions that apply: 1, 4.
- Quote, decision 1: "Every security-relevant path has at least two independent layers. Each layer must stop the attack alone, without help from the others."
- Quote, decision 4: "The test sends hostile input directly to that layer, as if every other layer had failed."
- Effect: the supply-chain parts of the change (the pins, the permissions, the checksum check and the upload) are security code. "Layers" in `design.md` lists each layer and its hostile test.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Draft or published release? A draft. ADR 283 decision 3 says the release ships the archives, and it names no release type. A draft changes nothing for an operator until a person publishes it, and a person can delete it. A published release cannot be undone without a public trace. No ADR asks for an automatic publish. The change keeps the draft step as one `--draft` flag, so the owner can ask for another type later with a one-line change.
- Does the chart belong to this change? No. The workflow pushes it already, and ADR 246 decision 3 says it ships as an OCI artifact in the registry. A release page file would be a second copy with a second checksum story. See "Decisions" in `design.md`.
- Sign the archives? No. ADR 283 decision 4.
- Which job runs first, the image or the archives? The archives. The archives are built and checked before the first push to a registry, because a registry push is public and cannot be taken back. The draft is created last, because it is the one step that a person can undo.
- Where does the dry run run? In the Go job of every pull request, as tests. No tag and no workflow run are needed. See "Test path with no tag" in `design.md`.
- Does the change tag or release? No. See ADR 279 above.

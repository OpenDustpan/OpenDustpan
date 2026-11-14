---
version: 3
---

Issue: #945.

## Why

**Persona: operator.** An Operator installs v0.1.0 from a container image. The Operator tries to pull a known image tag from the forge container registry or from Docker Hub and install it with Helm. The registry choice of ADR 27 was left open, and no workflow publishes to both registries, so the Operator cannot rely on one pull path.

The user path this change must make work:

1. **Docs page.** Operator opens the install page and copies the pull command for the chosen registry.
2. **CLI.** Operator pulls the pinned tag from the forge container registry or from Docker Hub.
3. **CLI.** Operator installs with Helm using the chart default registry or the override value.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Push to Docker Hub fails with authentication denied when the Docker Hub token secret is missing. | The workflow annotation names the missing secret and the one setup step. | `gh secret set DOCKERHUB_TOKEN --repo OWNER/REPO` |
| Push to Docker Hub fails with authentication denied when the Docker Hub token is expired or has no push scope. | The workflow annotation names the rotation step and fails the run. | `gh secret set DOCKERHUB_TOKEN --repo OWNER/REPO` |
| Digest comparison fails when the two registries hold different digests. | The comparison step prints both digests and fails the run. | `docker buildx imagetools inspect <image>:<tag>` |

Evidence for the claims above:

- Issue 945 scopes OS packages out and requires a person to add the Docker Hub token before the first run. Proof: Out: OS packages (separate issue). Needs a person to add the Docker Hub token as a repository secret before the first run.
- Issue 945 decides distribution for v0.1.0 as the forge container registry plus Docker Hub. Proof: 35.3 (owner, 2026-10-18): distribution for v0.1.0 is the forge container registry plus Docker Hub. The Docker Hub organization is registered.
- Issue 945 requires one digest on both registries with a test that pulls each and compares digests. Proof: A tag push publishes one digest to both registries; a test step pulls each and compares digests.

## What Changes

- The release workflow builds once for linux/amd64 and linux/arm64 and pushes the same digest to the forge container registry and then to Docker Hub.
- A digest comparison step pulls each registry reference and fails when digests differ.
- The Helm chart default image repository names the forge container registry, with a value override for Docker Hub.
- The install docs page lists both pull commands for v0.1.0 and the Docker Hub token setup note.

Out of scope:

- OS packages
- Image signatures and SBOM in v0.1.0
- ACME and Helm chart multi-replica work

Depends on:

- none

Builds on (not a change):

- Helm chart multi-replica work (W7): this change shares one image repository values key with it

## Usability

Where the path still costs the person time or surprises them:

- The Operator finds both pull commands on one install page.
- The first run needs a person to add the Docker Hub token secret, and the workflow error names that one setup step.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main already has an e2e image check that fails closed on image problems. (`e2e/image_test.go:72 (func TestImageFailsClosed)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 27: TLS and release artifacts** (not owner-tagged).

- Decisions that apply: 5 (current), 6 (changed), 7 (extended).
- Changed by: ADR 212 changes decision 2; ADR 246 extends decision 8; ADR 278 changes decision 6; ADR 283 changes decision 8; ADR 283 extends decision 7. Chain to the end: ADR 212, ADR 246, ADR 278, ADR 283, ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 315, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 212, 246, 278, 283.
- Quote: "The base image is distroless static `nonroot`, pinned by digest."
- Quote: "The image release workflow needs the GitHub Actions `docker/setup-buildx-action` and `docker/setup-qemu-action`."
- Effect: Follows the multi-arch image and the buildx gate. The registry choice that decision 8 left open is fixed by the owner decision in issue 945.
- Conflict: none.

**ADR 283: Packaging and installation for v0.1.0** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current), 5 (current), 6 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "`v0.1.0` ships the `SHA256SUMS` file of ADR 27 decision 6. It ships no signature of the archives or of the image, and no SBOM."
- Quote: "The image release workflow uses them to build linux/amd64 and linux/arm64."
- Quote: "A `v*` tag starts the workflow of the binaries and the image."
- Effect: Follows the owner rules: no image signature and no SBOM in v0.1.0, buildx and QEMU for both architectures, a v tag starts the workflow. The issue scope line on signatures and SBOM is superseded, and Q2 asks the owner to confirm it.
- Conflict: none.

**ADR 246: A Helm chart for `opendustpan serve` with an external PostgreSQL** (not owner-tagged).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The tag workflow pushes the chart as an OCI artifact to the registry of the image."
- Effect: The chart goes as an OCI artifact to the forge container registry, the registry of the image.
- Conflict: none.

**ADR 214: A user-facing feature needs its docs page in the same pull request** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 315 extends. Chain to the end: ADR 315.
- Current rule: this ADR, with ADR 315.
- Quote: "A pull request that adds or changes a feature that a person or a client can see adds or changes its page in the user documentation."
- Effect: The install page with both pull commands ships in the same pull request.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What are the exact repository paths of the forge registry and of the Docker Hub organization?
   - Options: Forge path from the repository name, Docker Hub path from the registered organization; Another path set by the owner.
   - Provisional decision (owner review 14 Nov): Use the repository name for the forge path and the registered organization for Docker Hub, and give both paths in the release workflow as named values.
   - Reason: Issue 945 says the Docker Hub organization is registered but gives no name. The pack names no path. Benchmark: Google Artifact Registry practice: one fixed, named path for each artifact, written in the install docs.
   - Persona impact: No persona loses access: both registries hold the same digest. Escape hatch: An operator overrides the image repository value in Helm values.
   - Owner-tagged ADRs involved: ADR 283 (their words do not change).
2. Does the owner confirm that v0.1.0 ships no image signature and no SBOM, so the issue scope line is corrected?
   - Options: Follow ADR 283 decision 4 and correct the issue line; Change the owner decision to add signatures and an SBOM.
   - Provisional decision (owner review 14 Nov): Follow ADR 283 decision 4 and correct the issue line.
   - Reason: Issue 945 scope says signatures and SBOM attach to both registries. ADR 283 decision 4 (owner) says the opposite. Benchmark: Google engineering practices for code review: one source of truth for scope, corrected before merge.
   - Persona impact: No persona loses access: SHA256SUMS stays in both options. Escape hatch: Operators verify artifacts with the SHA256SUMS file.
   - Owner-tagged ADRs involved: ADR 283 (their words do not change).

Questions considered and answered:

- Does this change attach signatures and SBOM to both registries as the issue scope says? No. ADR 283 decision 4 (owner) ships no image signature and no SBOM in v0.1.0. The change follows it. Q2 records the owner confirmation.
- Is the buildx and QEMU gate of ADR 27 decision 7 answered? Yes. ADR 283 decision 5 (owner) gives the yes.
- Can the docs page land later? No. ADR 214 decision 1 needs the page in the same pull request.
- Is the chart default registry open? No. ADR 246 decision 3 puts the chart in the registry of the image. The change sets the forge registry as default, and the override value gives Docker Hub. Only the exact paths remain in Q1.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

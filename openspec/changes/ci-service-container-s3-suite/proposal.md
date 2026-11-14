---
version: 2
---

Issue: #183.

## Why

**Persona: developer.** A developer keeps the S3 blob store correct, but CI skips the pkg/blob/s3 suite today so a regression lands unseen. This change runs the existing suite in CI on a pinned S3-compatible server, with no new action and no server change.

The user path this change must make work:

1. **Console.** Open the pull request and read the CI go job result
2. **CLI.** Search the go test log for pkg/blob/s3 and confirm blobtest.Run passed with no skip

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| RustFS service container does not answer the S3 API | The go job fails at the S3 readiness wait step. Fix the image digest or the registry access, then rerun the job. | `gh run view --log \| grep -i rustfs` |
| Bucket setup step fails before go test starts | The go job fails at the bucket step. Fix the endpoint or the AWS credential exports, then rerun the job. | `AWS_ACCESS_KEY_ID=dustpan-test AWS_SECRET_ACCESS_KEY=<test-value> aws s3api create-bucket --endpoint-url http://localhost:9000 --region us-east-1 --bucket dustpan-ci-s3` |
| S3 suite skips although the container runs | The go job fails at the no-skip guard. Fix the four DUSTPAN_TEST_S3_* variables or the endpoint, then rerun the job. | `go test ./pkg/blob/s3/... -v -count=1` |

Evidence for the claims above:

- The shared blob conformance entry point is blobtest.Run Proof: pkg/blob/blobtest/blobtest.go:46 (func Run)
- pkg/blob/s3 has a store test for the not-found mapping Proof: pkg/blob/s3/store_test.go:671 (TestGetNotFoundMapping)
- Issue #183 asks for a RustFS service container pinned by digest with the four DUSTPAN_TEST_S3_* variables in the go job Proof: Issue #183 body: service container, health check, four DUSTPAN_TEST_S3_* variables, digest sha256:1803faef57627e2d9c2e7d89d655d712ddded5389040054987163043fecb6a3c

## What Changes

- Add a RustFS service container pinned by digest to the go job in .github/workflows/ci.yml with an S3 API readiness wait
- Set the four DUSTPAN_TEST_S3_* variables for the job and make the bucket with aws cli in a prior step
- Run go test -race ./... so pkg/blob/s3 runs blobtest.Run in CI with a guard that fails the job on skip

Out of scope:

- Server code changes
- New S3 adapter behavior or new blobtest cases
- Helm, replicas or milestone 7 exit-test steps
- Any new GitHub Action

## Usability

Where the path still costs the person time or surprises them:

- A contributor copies one service block and four variable names from ci.yml to run the same RustFS image locally, but a digest recheck at work start costs one lookup of the RustFS digest.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The shared blob conformance runner exists as blobtest.Run (`pkg/blob/blobtest/blobtest.go:46 (func Run)`)
- pkg/blob/s3 already has a store test for the not-found mapping (`pkg/blob/s3/store_test.go:671 (func TestGetNotFoundMapping)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 27: TLS and release artifacts** (not owner-tagged).

- Decisions that apply: 7 (extended).
- Changed by: ADR 212 changes decision 2; ADR 246 extends decision 8; ADR 278 changes decision 6; ADR 283 changes decision 8; ADR 283 extends decision 7. Chain to the end: ADR 212, ADR 246, ADR 278, ADR 283, ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 315, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 212, 246, 278, 283.
- Quote: "Gate for new tools. The image release workflow needs the GitHub Actions `docker/setup-buildx-action` and `docker/setup-qemu-action`. CLAUDE.md asks for the owner's yes before the project adds them."
- Effect: Decision 7 gates only the buildx and QEMU actions for the image release workflow. It does not gate a service container. The owner yes for this change comes from ADR 31 decision 8 alone. The change adds no GitHub Action.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does the owner say yes to RustFS pinned by digest as a new test tool in CI?
   - Options: Allow the pinned RustFS image by digest as the S3 service container; Refuse any new test tool in CI and keep the S3 suite local-only.
   - Provisional decision (owner review 14 Nov): Allow the pinned RustFS image by digest
   - Reason: ADR 31 decision 8 says a new test tool in CI waits for the owner yes, and issue #183 says work needs the owner yes before it starts. Benchmark: Google: pin third-party CI images by digest and recheck the digest at work start before first use.
   - Persona impact: No persona loses function. A refusal keeps the current skip behavior that developers already use. Escape hatch: If the owner refuses RustFS, CI keeps skipping the S3 suite and a developer runs it on a local server, per ADR 31 decision 8.

Questions considered and answered:

- Does the S3 suite run in CI or stay local-only? CI runs it once the owner says yes. ADR 31 decision 8 says CI runs the S3 suite on a service container, and until then a developer runs the suite on a local server and CI skips it.
- Does the change need a new GitHub Action? No new action. ADR 31 decision 8 says a container needs no GitHub Action.
- Who must say yes to the new test tool in CI? The owner decides, per ADR 31 decision 8. The change stays blocked until the owner says yes, and the open question records that gate.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

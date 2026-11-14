## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing release test for versioned multi-arch image build with pinned console hash in the NEW file `e2e/release_image_test.go`. The test runs an acceptance path on no API route: BLOCKED by the missing route, because the image build has no API route and no credential kind. It calls the NEW script `scripts/release-build-image.sh`. Check: TestReleaseImageBuildsFromVersion fails on main, passes after.
- [ ] 1.2 Add failing artifact install test that starts the built image and runs the operator persona path in the NEW file `e2e/release_artifact_install_test.go`. The test calls the existing route `ANY/GET /api/dustpan/v1/git-host-installations` with a session credential. Check: TestReleaseArtifactInstallRunsPersonaPath fails on main, passes after.

## 2. Build

- [ ] 2.1 Add the NEW script `scripts/release-build-image.sh` with VERSION input, buildx, QEMU, and console hash check. Check: `scripts/release-build-image.sh 0.1.0-test` builds local multi-arch image with no push.
- [ ] 2.2 Add the NEW file `.github/workflows/release-image.yml`. It is a workflow that starts on v tag and reuses binary archive outputs without push. Check: Workflow file pins both setup actions by SHA with version tag comment and holds no push step.
- [ ] 2.3 Add the NEW Make target `make image-smoke` in the NEW file `Makefile.image` (NEW, included by the existing `Makefile`) with artifact install test step for the built image and the part 1 binary archive. Check: `make image-smoke VERSION=0.1.0-test` starts image and passes health and console checks.
- [ ] 2.4 Add the NEW file `docs/install-from-artifacts.md` as an install-from-artifacts docs page with image install, binary archive install, and verify steps. Check: Docs page runs each command as written in test.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Wire image tag to server --version stamp via ldflags. Check: Image exposes no new route; route test unchanged and no API description change.
- [ ] 4.2 Add e2e step for install from built image against the existing route with a session credential. Check: e2e step calls the existing route `ANY/GET /api/dustpan/v1/git-host-installations`, passes the authorization gate, with a session credential.
- [ ] 4.3 Run usability check of install-from-artifacts page. Check: Operator follows docs page alone and completes install from image.
- [ ] 4.4 Run full checks and fix findings. Check: `make check` with zero findings.

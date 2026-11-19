## Context

The operator installs from built artifacts. The archive build, the archive check, the image build, the image smoke test and the tag workflow exist on main. The gaps are three: the version is not an input of the local image scripts and the tag workflow, the console pin placeholder fails with an unclear message, and no test starts a server from a built archive.

## Decisions

1. **One version input for the image scripts: `IMAGE_VERSION`.** The scripts read settings from the environment today (`scripts/build-image-local.sh` header). The change keeps that and adds one variable. The default stays `dev` for the build script and `smoke` for the smoke test. The release form is `v` followed by three numbers, the same value as the tag and as the archive stamp. `make image-smoke VERSION=v0.1.0` passes the value as `IMAGE_VERSION` only when `VERSION` is on the make command line, as `release-record-check` does (`Makefile:191-196`).
2. **Allowlist, not the release form only.** The existing Dockerfile header builds with `git describe --tags --always --dirty`, for example `v0.1.0-3-gabc123-dirty`. The allowlist is `^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$`. It has no quote, no space, no dollar sign, no backtick and no newline, so the value is safe inside the `-ldflags "-X main.version=${VERSION}"` shell line. The check refuses; it never edits the value.
3. **Two independent layers.** The scripts check the value before any `docker` call. The Dockerfile checks the same value again in the Go stage, so that a direct `docker build --build-arg VERSION=...` cannot skip the script. Each layer has its own test.
4. **The placeholder pin is refused by name.** The console stage refuses a `CONSOLE_SHA256` of 64 zeros with a message that names the Dockerfile pin and the two build arguments. The existing `sha256sum -c` check stays as the check of a real pin. No new build argument: the argument stays `CONSOLE_SHA256`.
5. **The workflow change is a patch.** The release workflow passes `--build-arg VERSION="${TAG}"` and the two console arguments from the Dockerfile defaults (it adds no second source of the pin). The cloud agent cannot push `.github/workflows`, so the diff goes into the pull request body under "Workflow patch (apply locally)". A test reads the workflow file and goes green when a person applies the patch.
6. **The archive test builds in a copy of the tree.** It reuses `copyWorkTree`, `extendRecord` and `runInTree` of `internal/releaserecord/release_archives_test.go`, because `release-build.sh` refuses a version that the release record does not list. It reads the host archive with `archive/tar` into an `os.Root` directory. It runs the unpacked binary with an argument list and no shell.
7. **The docs commands run in that test.** The section "Install from a binary archive" has a download block and two blocks for the check and the unpack. The test takes the two blocks from the page, replaces the placeholders and runs them with `sh` in the directory of the built set. The download block needs a published release and is not run. The page stays in the pending list until a docs-check skip reason covers a download (not in this change).

## Structure

No new type and no new script. The guard is the allowlist in the two existing scripts and in the Dockerfile. A Go type cannot remove the need: the value comes from an environment variable and a build argument of a shell and a build tool.

## Risks

- The archive test builds four platforms. It is skipped with `-short`, as `TestReleaseArchivesAreReproducible` is. It may add one build time of the existing test.
- The test of the workflow file fails on the pull request until a person applies the patch. The pull request stays a draft until then.
- The real console pin does not exist yet. A tag build stops at the Dockerfile guard until the pin lands.
- The docs blocks of the archive section may drift from the test. The test reads the page, so a change of the page changes the test input.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| version match | 100 | % of built artifacts where `--version` equals the version input | one image and one archive binary of one version |
| refusal before build | 0 | `docker` calls on a bad version | `build-image-local.sh` and `smoke-image.sh` |
| archive check | 100 | % of runs where the host archive matches its `SHA256SUMS` line before it starts | one archive test run |

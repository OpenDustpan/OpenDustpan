## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [x] 1.1 Add failing test in the NEW file internal/releaserecord/release_archives_test.go (package releaserecord_test, NEW; it builds in a temporary copy of the work tree whose release record the test extends with a line for the test version) for sorted tar order, fixed owner mode mtime, gzip -n level 9, and byte equal rebuild Check: TestReleaseArchivesAreReproducible (NEW symbol, not on main) fails on main because scripts/release-build.sh:60 runs tar -czf with no sorted order, fixed owner or mtime and no gzip -n, passes after
- [x] 1.2 Add failing test in the NEW file internal/releaserecord/release_verify_test.go (package releaserecord_test, NEW) for bad version, missing license, missing or extra file, checksum mismatch, traversal entry, and stamp mismatch without execution Check: TestReleaseVerifyRefusesBadInput (NEW symbol, not on main) fails on main because scripts/release-verify.sh (NEW) does not exist, passes after

## 2. Build

- [x] 2.1 Change the script scripts/release-build.sh in place (no second build script; keep the record check before the first `go build`, the names `opendustpan_<version>_<os>_<arch>.tar.gz`, and the four platforms), and add the NEW Make target release-build in Makefile that calls it with VERSION and an output directory, with CGO_ENABLED=0, -trimpath, -buildvcs=false, and ldflags stamp Check: `go test ./internal/releaserecord -run 'TestReleaseArchivesAreReproducible|TestReleaseBuildChecksTheRecordFirst' -count=1` passes, and `make release-build VERSION=v0.1.0` refuses with the record message until the release record lists v0.1.0 (the tag step adds that line)
- [x] 2.2 Add the NEW verify script scripts/release-verify.sh (NEW) and the NEW Make target release-verify in Makefile with read only fail closed checks and string based stamp check Check: The NEW Make target `make release-verify VERSION=v0.1.0` passes on a good set and fails on each corrupt set

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run definition of done for this part: wiring of both NEW Make targets and usability check from test output; route, e2e install step, and docs page are BLOCKED by part 2 container image and install docs work Check: BLOCKED by part 2 container image, install test route and docs page; `make check` with zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).

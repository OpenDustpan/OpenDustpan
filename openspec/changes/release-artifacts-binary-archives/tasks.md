## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test in the NEW file scripts/release/release_build_test.go (package release, NEW) for sorted tar order, fixed owner mode mtime, gzip -n level 9, and byte equal rebuild Check: TestReleaseArchivesAreReproducible fails on main, passes after
- [ ] 1.2 Add failing test in the NEW file scripts/release/release_verify_test.go (package release, NEW) for bad version, missing license, missing or extra file, checksum mismatch, traversal entry, and stamp mismatch without execution Check: TestReleaseVerifyRefusesBadInput fails on main, passes after

## 2. Build

- [ ] 2.1 Add the NEW build script scripts/release/build.sh (NEW) and the NEW Make target release-build in Makefile with CGO_ENABLED=0, -trimpath, -buildvcs=false, and ldflags stamp Check: The NEW Make target `make release-build VERSION=v0.1.0` writes four archives plus SHA256SUMS
- [ ] 2.2 Add the NEW verify script scripts/release/verify.sh (NEW) and the NEW Make target release-verify in Makefile with read only fail closed checks and string based stamp check Check: The NEW Make target `make release-verify VERSION=v0.1.0` passes on a good set and fails on each corrupt set

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run definition of done for this part: wiring of both NEW Make targets and usability check from test output; route, e2e install step, and docs page are BLOCKED by part 2 container image and install docs work Check: BLOCKED by part 2 container image, install test route and docs page; `make check` with zero findings

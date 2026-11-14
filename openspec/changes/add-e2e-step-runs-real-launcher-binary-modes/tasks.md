## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests and build

- [ ] 1.1 Write failing tests for static build, strict flags with code 64, copy mode and both helper modes. Check: TestStaticBinary fails on main
- [ ] 1.2 Write failing tests for run order, exit 0, exit 65, handoff channels, URL checks, default dirs. Check: TestLauncherRunOrder fails on main
- [ ] 1.3 Write failing tests for credentials, timeouts, masking and secrets directory. Check: TestLauncherCredentials fails on main
- [ ] 1.4 Wire cmd/dustpan-launcher to the four modes with no new logic in cmd. Check: TestLauncherParseUsage
- [ ] 1.5 Add the release build, static check and dependency check. Check: go list -deps check fails on a SQL driver import

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Add the launcher docs page. Check: make docs-lint
- [ ] 3.2 Run mode tests on amd64 and arm64 builds and the license check. Check: make licenses
- [ ] 3.3 Usability check: BLOCKED by the sibling part 3 change, which adds the real runner e2e step and the route and credential kind for it. Check: BLOCKED by add-e2e-step-runs-real-runner
- [ ] 3.4 Run make check. Check: `make check` reports zero findings

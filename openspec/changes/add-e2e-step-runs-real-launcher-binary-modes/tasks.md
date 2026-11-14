## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests and build

- [ ] 1.1 Write failing tests for static build, strict flags with code 64, copy mode and both helper modes in cmd/dustpan-launcher/binary_test.go. Check: TestStaticBinary fails on main
- [ ] 1.2 Write failing tests for run order, exit 0, exit 65, handoff channels, URL checks, default dirs in pkg/launcher/launcher_test.go. Check: TestLauncherRunOrder fails on main
- [ ] 1.3 Write failing tests for credentials, timeouts, masking and secrets directory in pkg/launcher/volume_test.go. Check: TestLauncherCredentials fails on main
- [ ] 1.4 Wire cmd/dustpan-launcher to the four modes with no new logic in cmd. Check: TestLauncherParseUsage
- [ ] 1.5 Add the release build, static check and dependency check in cmd/dustpan-launcher/deps_test.go. Check: go list -deps check fails on a SQL driver import

## 2. Side effects (computed from main)

- [x] 2.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 3. Definition of done

- [ ] 3.1 Add the launcher docs page in the NEW file docs/guides/launcher.md. Check: make docs-lint
- [ ] 3.2 Run mode tests on amd64 and arm64 builds and the license check. Check: make licenses
- [ ] 3.3 Usability check: BLOCKED by the sibling part 3 change, which adds the real runner e2e step and the route and credential kind for it. Check: BLOCKED by add-e2e-step-runs-real-runner. A person's step. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.4 Run make check. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).

## 1. Build the modes

- [ ] 1.1 Add failing tests for static build, strict flags with code 64, copy mode, and both helper modes. Check: TestStaticBinary fails on main, passes after the fix.
- [ ] 1.2 Add failing tests for the run order, exit 0, and exit 65 with no step start. Check: TestLauncherRunOrder fails on main, passes after the fix.
- [ ] 1.3 Add failing tests for SIGINT, grace periods, and SIGTERM handling. Check: TestLauncherStepTimeout fails on main, passes after the fix.
- [ ] 1.4 Add failing tests for cross-chunk masking and the 4-byte limit. Check: TestLauncherMasking fails on main, passes after the fix.
- [ ] 1.5 Add failing tests for tmpfs check, modes, owner, and per-step cleanup. Check: TestLauncherSecretsDir fails on main, passes after the fix.
- [ ] 1.6 Wire cmd/dustpan-launcher to launcher.Parse and the four modes with no new logic in cmd. Check: TestLauncherParseUsage lists the four modes and the run flags.
- [ ] 1.7 Add the release build and the static binary check. Check: Release build for linux amd64 and linux arm64 plus file static check pass.
- [ ] 1.8 Add the dependency check for SQL drivers and forge clients. Check: go list -deps check fails on a SQL driver or forge client import, including a transitive import.

## 2. Definition of done

- [ ] 2.1 Add the user docs page for the launcher modes and flags. Check: Docs page for the launcher renders and each command runs as written.
- [ ] 2.2 Run the mode tests on both release architectures. Check: Mode tests pass on amd64 and arm64 release builds.
- [ ] 2.3 Run the license check for the launcher binary. Check: make licenses finds the license files of each linked module.
- [ ] 2.4 Run the usability check with a runner operator. Check: Usability walkthrough with a runner operator completes from the docs page alone and each error names its fix.
- [ ] 2.5 Run make check with zero findings. Check: `make check` reports zero findings with licenses, static, and dependency checks included.

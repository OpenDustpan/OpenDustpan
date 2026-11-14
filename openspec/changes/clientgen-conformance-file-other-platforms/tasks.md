## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fix

- [ ] 1.1 Add a regression test in the NEW file internal/clientgen/conformance/process_tags_test.go that refuses process_group_other.go, refuses any file with a negated build tag, and refuses more than one definition of endGroupAfterExit Check: NEW test TestNoOtherPlatformProcessFile in internal/clientgen/conformance fails on main and passes after the fix. No route: the test reads package files only.
- [ ] 1.2 Delete process_group_other.go, move endGroupAfterExit to the NEW file process_group.go with no build tag, and remove the per-OS copies and unused waitid constants in process_linux.go and process_darwin.go Check: go test ./internal/clientgen/conformance/ passes on Linux and macOS. No route: the test runs the package suite only.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Run the full check with zero findings. Confirmed by: CI (make check). Check: make check reports zero findings. Confirmed by: CI (make check).

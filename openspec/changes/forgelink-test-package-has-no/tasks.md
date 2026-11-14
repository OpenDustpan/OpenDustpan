## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fix

- [ ] 1.1 Confirm the existing package clause and that no TestMain exists in pkg/forgelink test files, then add the NEW file pkg/forgelink/leak_test.go with TestMain that calls goleak.VerifyTestMain. No route: the test guard runs in the test binary only. Check: rg -n TestMain pkg/forgelink/*_test.go shows only the NEW TestMain after the change
- [ ] 1.2 Prove requirement fail-on-leaked-goroutine with a temporary leaked goroutine that fails the package, remove the temporary leak, then fix each leak the guard exposes in forgelink test code with no ignore option. No route: the test reads the test binary result only. Check: go test ./pkg/forgelink -count=1 fails with the temporary leak, then passes after it is removed

## 2. Definition of done

- [ ] 2.1 Run the forgelink package tests and confirm the leak guard runs. No route: the test reads the test binary result only. Check: go test ./pkg/forgelink -count=1
- [ ] 2.2 Run the definition of done check with zero findings. Confirmed by: CI (make check). Check: make check with zero findings

## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests first

- [ ] 1.1 Add failing ECDSA P-256 vectors and hostile-input table in NEW file internal/sshsig/verify_ecdsa_test.go. Check: `go test ./internal/sshsig/ -run TestVerifyECDSA -count=1` fails on main, passes after.
- [ ] 1.2 Add failing sk-ECDSA vectors and flag-rule hostile-input table in NEW file internal/sshsig/verify_skecdsa_test.go. Check: `go test ./internal/sshsig/ -run TestVerifySkECDSA -count=1` fails on main, passes after.
- [ ] 1.3 Add failing RSA vectors and hostile-input table for signature type and short modulus in NEW file internal/sshsig/verify_rsa_test.go. Check: `go test ./internal/sshsig/ -run TestVerifyRSA -count=1` fails on main, passes after.

## 2. implement

- [ ] 2.1 Implement ECDSA, sk-ECDSA, and RSA verification with size and exponent bounds in internal/sshsig/verifier.go:63, with wire limits and size checks before any RSA verify call. Check: `go test ./internal/sshsig/... ./internal/stepup/... -count=1` passes, including stepuptest SmallRSA refusal.
- [ ] 2.2 Extend testdata generator internal/sshsig/testdata/gen/generate.sh with real ssh-keygen vectors and skdummy.c ECDSA support. Check: `ls internal/sshsig/testdata` holds the NEW ECDSA, sk-ECDSA, and RSA vectors made by generate.sh.
- [ ] 2.3 Add the NEW ECDSA, sk-ECDSA, and RSA vectors as seeds to the fuzz test in NEW file internal/sshsig/verify_fuzz_test.go. Check: `go test ./internal/sshsig/ -run FuzzVerify -count=1` passes with the new seeds.
- [ ] 2.4 Measure one 8192-bit RSA Verify within the 500 ms quality target in NEW file internal/sshsig/verify_rsa_cost_test.go. Check: `go test ./internal/sshsig/ -run TestVerifyMaxRSA -count=1` passes within 500 ms.

## 3. definition of done

- [ ] 3.1 Run affected suites and the stepuptest verifier suite in internal/stepup/stepuptest/verifier.go:41. Check: `go test ./internal/sshsig/... ./internal/stepup/... -count=1` passes.
- [ ] 3.2 Run the full check gate. Confirmed by: CI (make check). Check: `make check` reports zero findings.

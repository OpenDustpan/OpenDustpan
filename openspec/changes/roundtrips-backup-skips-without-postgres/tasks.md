## 1. Skip in the parent test

- [ ] 1.1 No route: the test measures database round trips only. In `TestRoundTripsOfBackup` (internal/servertest/roundtrips_test.go:1148), skip at the start, before `t.Cleanup`, when `os.Getenv(servertest.EnvURL)` is empty, with the message of `startCluster` (internal/servertest/servertest.go:139). Check: with `DUSTPAN_TEST_POSTGRES_URL` unset, `go test ./internal/servertest/ -run TestRoundTripsOfBackup -v -count=1` reports SKIP; with it set, the test runs and passes.

## 2. Definition of done

- [ ] 2.1 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).

## 1. Test first

- [ ] 1.1 Add `TestSharedFetcherIsOneForEachDir` (NEW) in `e2e/fetch_test.go`: two calls of the NEW function `e2e.SharedFetcher` with one directory give the same Fetcher, two directories give two, and calls from parallel goroutines give one Fetcher for each directory. No route: the test calls the function only. Check: `go test -tags e2e ./e2e/ -run TestSharedFetcherIsOneForEachDir -count=1` fails on main.

## 2. Build

- [ ] 2.1 Add `SharedFetcher` (NEW) to `e2e/fetch.go`, with a registry guarded by a mutex, and make `sharedFetcher` (`e2e/e2e_test.go:97`) call it in place of its own map (remove `fetchers` and `fetchersMu`). Check: `go test -tags e2e ./e2e/ -run 'Fetch|SharedFetcher' -count=1` passes.
- [ ] 2.2 In `e2e/path_quickstart_test.go:54`, take the Fetcher from `sharedFetcher(t)` in place of a new `e2e.Fetcher`. No route: the test harness installs the CLIs only. Check: `go vet -tags e2e ./e2e/` exits 0.

## 3. Definition of done

- [ ] 3.1 Run the checks. No route: the test harness installs the CLIs only. Check: `make check` reports zero findings, and `make e2e` passes. Confirmed by: CI (the agent runs the fast checks only).

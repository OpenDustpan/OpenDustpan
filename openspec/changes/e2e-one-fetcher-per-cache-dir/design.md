## Context

Short-form bug in the e2e harness. The tests share one CLI cache directory,
and one test made a second Fetcher on it.

## Contract

- NEW function `e2e.SharedFetcher(cacheDir string, client *http.Client) *Fetcher` in `e2e/fetch.go`: it returns the one Fetcher of cacheDir in the process, and makes it on the first call with client. It returns no error.
- `sharedFetcher` of the tests (`e2e/e2e_test.go`) calls it. No other type, route or output changes.

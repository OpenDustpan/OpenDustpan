---
version: 1
---

Issue: #3178.

Short form: bug. Owner lane: M9.

## Problem

**Persona: contributor.** A contributor whose e2e run fails with "pulumi: no
such file or directory" loses a CI round to a failure that the change did not
cause. A likely cause: one test can delete a cached CLI that another test
runs. The tests share one cache directory and are meant to share one
`e2e.Fetcher` for it, but one test makes its own. That second Fetcher checks
the entry again, and when its check refuses the entry it deletes the
directory and downloads again. The file lock covers the check and the
download, not the use.

## Evidence

- `e2e/e2e_test.go:70` (func cacheDir) gives one directory for the tests, and
  `e2e/e2e_test.go:97` (func sharedFetcher) keeps one Fetcher for each
  directory ("so that each process checks each cached CLI one time").
- `e2e/path_quickstart_test.go:54` makes `&e2e.Fetcher{Client: http.DefaultClient,
  CacheDir: cacheDir(t)}`, a second Fetcher on the same directory, with an
  empty map of checked entries.
- `e2e/fetch.go:167` checks the entry again for a Fetcher that has not checked
  it, and `e2e/fetch.go:173` calls `removeCacheEntry` (:717) on a refusal,
  which deletes the extracted directory and the archive.
- `lockEntry` (`e2e/fetch.go:144`) is released when the install returns, so
  it does not protect a test that runs the CLI.

## Fix boundary

- The `e2e` package holds the registry: the NEW function
  `e2e.SharedFetcher(cacheDir string, client *http.Client) *Fetcher` returns
  the one Fetcher of a cache directory in the process. `sharedFetcher` of the
  tests calls it, and `path_quickstart_test.go` calls `sharedFetcher`.
- So a second check of a checked entry in the same process cannot happen,
  and no test of the process deletes a CLI that another test of the process
  runs.
- The tests of the Fetcher itself (`e2e/fetch_test.go`) keep their own
  Fetchers on their own temporary directories.
- No retry, no skip.

## Check

`go vet -tags e2e ./e2e/` and `go test ./e2e/ -run 'Fetch|SharedFetcher' -count=1`
pass, and `make e2e` passes (Confirmed by: CI).

## ADR check

No ADR is touched. Conflict: none.

## Open questions

none. Considered: (1) retry the CLI run (refused: it hides the race); (2) a
shared lock held for the whole use of a CLI (refused for now: one process
runs the e2e suite, so one Fetcher for each directory removes the race with
less code; a second process on one cache directory is a later question).

# CI fixes pending on main (2583f66, 2026-10-30 19:55 UTC)

From run 41113548820 (push of 2583f66). The jobs store (postgres), store (sqlite), e2e and e2e-light pass.

## 1. Job `go`, step Test (make test-ci-other): FAIL internal/servertest

Error:

    --- FAIL: TestRoundTripsOfBackup/30_stacks_and_30_workspaces (9.53s)
    roundtrips_test.go:860: backup of 30 stacks and 30 workspaces: 311 round trips, budget 318
    roundtrips_test.go:861: restore of 30 stacks and 30 workspaces: 1103 round trips, budget 1102

Cause: the restore ceiling is fixed (1102, set by 96d9c09), and the restore count varies by about 25 between runs under load. This is issue #2115.

Fix: a cloud session is fixing #2115 now. It makes the ceiling a function of the backup table list, finds the cause of the variation, and adds a negative test for an N+1 loop. Do not raise the ceiling again by hand. If main must be green before that PR lands, a raise to the measured maximum plus a margin above the spread (about 30) is the stopgap, as its own commit that names #2115.

## 2. Job `scan`, step TruffleHog scan: FAIL

Error: two unverified URI findings in Git history, in old commits of pkg/source/filters_test.go (6b5b733) and pkg/trustrule/trustrule_test.go (b9c8075).

Fix: PR #2117 (owner-approved). It adds the two paths to .trufflehog-exclude-paths.txt with reasons, and rewrites the trust-rule test to build its URL at run time. It is in the queue of merge session 21. No local action is needed.

## 3. Job `lint`, step Nil-panic analysis: cancelled

The step started at 19:34 and was cancelled at 19:39. A scheduled run on the same sha (41115253737) started at 19:26. Probable cause: concurrency cancellation, or the step time limit. Not confirmed. Fix: re-run the job, and look at the job log if it fails again.

## 4. Workflow header comments (not a failure)

The headers of .github/workflows/e2e-nightly.yml, cli-release-watch.yml and e2e-ha-nightly.yml still say "Not active here" and describe a TestDriftAPI failure that is fixed (3311785). The diff is in issue #2119. Cloud sessions cannot push it, because their token has no workflow scope. A session with the workflow scope can apply the diff from #2119 as it is (comment lines only, actionlint clean).

Also stale in cli-release-watch.yml: "Until they exist in main, the job finds no known version". The contract files in e2e/contract/ are on main.

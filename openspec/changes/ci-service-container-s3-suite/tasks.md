## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. CI wiring

- [ ] 1.1 Confirm the owner gate on issue #183 (NEW record in the pull request text for .github/workflows/ci.yml, no file edit): do no work until the owner says yes to RustFS. Write no edit to .github/workflows/ci.yml before then. Check: Owner yes recorded on issue #183 before any edit. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] 1.2 Record the failing baseline in the pull request text (NEW note, no file edit): show the S3 suite in pkg/blob/s3/store_test.go skips without the four variables. Check: `go test ./pkg/blob/s3/... -v` on main without variables shows --- SKIP

## 2. Bucket and readiness

- [ ] 2.1 In .github/workflows/ci.yml (the go job), add the RustFS service container to the go job and set DUSTPAN_TEST_S3_ENDPOINT, DUSTPAN_TEST_S3_BUCKET, DUSTPAN_TEST_S3_ACCESS_KEY and DUSTPAN_TEST_S3_SECRET_KEY. Check: `rg -n rustfs .github/workflows/ci.yml` shows the digest, ports 9000:9000 and the fixed test-only RUSTFS_ROOT_PASSWORD. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.
- [ ] 2.2 In .github/workflows/ci.yml (the go job, before the go test step), add steps that wait for the S3 API and then make the bucket with aws cli so an existing bucket passes. Check: CI log shows list-buckets readiness retries then create-bucket exits 0, and a rerun with an existing bucket still exits 0 (scenario Existing bucket passes). The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.

## 3. Definition of done

- [ ] 3.1 Show that go test -race ./... runs the S3 suite in CI and passes Check: CI log of `go test -race ./... -v` shows pkg/blob/s3 ran blobtest.Run to pass and the guard step exits 0
- [ ] 3.2 Prove the no-skip guard fails the job when the suite skips Check: Guard on the -v log exits 1 when the pkg/blob/s3 log holds --- SKIP or the package result is not ok

## 4. Docs and checks

- [ ] 4.1 Record the digest check with exact store, version and date in the pull request text for .github/workflows/ci.yml (NEW note, no file edit); the digest is pinned in .github/workflows/ci.yml. Check: Pull request text lists the RustFS digest, its version and the check date with no support claim beyond the test. The cloud agent cannot push .github/workflows. Write the full diff of the workflow file in the pull request description under the heading 'Workflow patch (apply locally)', and leave the PR as a draft. Confirmed by: A person's step.

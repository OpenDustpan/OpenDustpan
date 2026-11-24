## 0. Confirm against the code first

- [x] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. CI wiring

- [x] 1.1 Record the owner decision in the pull request text (NEW note, no file edit): ADR 376 decision 2 says "CI runs the S3 suite on RustFS 1.x, pinned by digest (owner, 2026-11-14)." Read the digest of the current 1.x release again and pin that digest in the staged file `.github-workflows-staging/ci.yml`. Owner approved 2026-11-21, explicitly not blocking for agents. Check: Pull request text cites ADR 376 decision 2 and lists the digest of the current RustFS 1.x release. A human moves the staged file into `.github/workflows/ci.yml` later.
- [x] 1.2 Record the failing baseline in the pull request text (NEW note, no file edit): show the S3 suite in pkg/blob/s3/store_test.go skips without the four variables. Check: `go test ./pkg/blob/s3/... -v` on main without variables shows --- SKIP

## 2. Bucket and readiness

- [ ] 2.1 Write the NEW staged file `.github-workflows-staging/ci.yml` as a full copy of `.github/workflows/ci.yml`, as `.github-workflows-staging/README.md` tells. In the go job of the staged file, add the RustFS service container and set DUSTPAN_TEST_S3_ENDPOINT, DUSTPAN_TEST_S3_BUCKET, DUSTPAN_TEST_S3_ACCESS_KEY and DUSTPAN_TEST_S3_SECRET_KEY. Check: `rg -n rustfs .github-workflows-staging/ci.yml` shows the digest, ports 9000:9000 and the fixed test-only RUSTFS_ROOT_PASSWORD. A human moves the staged file into `.github/workflows/ci.yml` later.
- [ ] 2.2 In the staged file `.github-workflows-staging/ci.yml` (the go job, before the go test step), add steps that wait for the S3 API and then make the bucket with aws cli so an existing bucket passes. Check: CI log shows list-buckets readiness retries then create-bucket exits 0, and a rerun with an existing bucket still exits 0 (scenario Existing bucket passes), after the staged file is moved into `.github/workflows/ci.yml`. A human moves the staged file into place later.

## 3. Definition of done

- [ ] 3.1 Show that go test -race ./... runs the S3 suite in CI and passes Check: CI log of `go test -race ./... -v` shows pkg/blob/s3 ran blobtest.Run to pass and the guard step exits 0. The check needs a CI run after the staged ci.yml is moved into `.github/workflows/ci.yml`.
- [ ] 3.2 Prove the no-skip guard fails the job when the suite skips Check: Guard on the -v log exits 1 when the pkg/blob/s3 log holds --- SKIP or the package result is not ok. The check needs a CI run after the staged ci.yml is moved into `.github/workflows/ci.yml`.

## 4. Docs and checks

- [x] 4.1 Record the digest check with exact store, version and date in the pull request text for the staged file `.github-workflows-staging/ci.yml` (NEW note, no file edit); the digest is pinned in the staged file. Check: Pull request text lists the RustFS digest, its version and the check date with no support claim beyond the test. A human moves the staged file into `.github/workflows/ci.yml` later.

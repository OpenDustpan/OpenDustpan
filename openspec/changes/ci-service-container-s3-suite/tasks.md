## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. CI wiring

- [ ] 1.2 Record the failing baseline in the pull request text (NEW note, no file edit): show the S3 suite in pkg/blob/s3/store_test.go skips without the four variables. Check: `go test ./pkg/blob/s3/... -v` on main without variables shows --- SKIP

## 2. Bucket and readiness

## 3. Definition of done

## 4. Docs and checks

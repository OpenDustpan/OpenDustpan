## 1. Remove the hash files and their writers

- [ ] 1.1 Delete `pkg/store/sqlite/migrations/0168_baseline.sql.sha256` and `pkg/store/postgres/migrations/0168_baseline.sql.sha256` with `git rm`. Check: `find pkg/store -path '*/testdata' -prune -o -name '*.sql.sha256' -print` prints nothing.
- [ ] 1.2 In `pkg/store/sqlite/frozen_test.go` and `pkg/store/postgres/frozen_test.go`, change the embed pattern at :21 to `migrations/*.sql`, and make `readFrozenHash` (:232) read `migrations/<name>.sha256` from the package directory with `os.ReadFile`, with the same error when the file is absent. Check: `go test ./pkg/store/sqlite/ -run 'Frozen|Migration' -count=1` passes, and `go vet ./pkg/store/postgres/` exits 0.
- [ ] 1.3 In `scripts/auto-regen.sh`, remove the two globs `pkg/store/postgres/migrations/*.sql.sha256` and `pkg/store/sqlite/migrations/*.sql.sha256` (:59-60), the function `write_hashes` (:205-216) and its call in `regenerate` (:229). Check: `rg -n 'sql\.sha256|write_hashes' scripts/auto-regen.sh` finds no line, and `bash -n scripts/auto-regen.sh` exits 0.

## 2. Definition of done

- [ ] 2.1 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).

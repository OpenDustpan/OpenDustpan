## 0. Triage
- [ ] 0.1 For each open PR that touches `pkg/store/`: merge, close as stale, or mark "port with branch mode". Check: a table of the 23 PRs and their outcome in STATE.md. Confirmed by: A person's step (the lead triages the open store PRs).
## 1. Prepare (PR B)
- [ ] 1.2 Add the branch mode to the NEW script `scripts/move-store.sh` of task 1.1. Check: it ports one open store PR branch and `go build ./...` passes on it.
- [ ] 1.1 Write the NEW script `scripts/move-store.sh` and run it on a fresh main. Check: `go build ./...` passes on the result, and `git diff --stat` shows only renames, import lines and generated files.
## 2. Constructors (PR A, can land first)
- [ ] 2.1 Add `dustpan.OpenStore`, `dustpan.BuiltinStore`, `dustpan.StorePorts` and `dustpan.WithBuiltinStore`; keep `dustpan.WithSQLite` and `dustpan.WithPostgreSQL`; make `cmd/opendustpan` use `OpenStore` and `WithBuiltinStore`. Check: the `pkg/dustpan` test of the proposal; `cmd/opendustpan` has no store wiring of its own.
## 3. Paths
- [ ] 3.1 Update `sqlc.yaml`, the Makefile, CI paths, `.golangci.yml`, the layout in `AGENTS.md`, the docs. Check: `sqlc diff` clean, `make docs-lint` exits 0.
## 4. Verify
- [x] 4.0 Not applicable: the baselines `pkg/store/sqlite/migrations/0168_baseline.sql` and `pkg/store/postgres/migrations/0168_baseline.sql` have no `.sha256` file until the first tag (ADR 362 decision 4; remove-baseline-sha256-files deletes them), so the move carries none.
- [ ] 4.1 Run the targets `check` and `e2e` of the `Makefile`. No route: the test runs the existing suites only. Check: `make check` reports zero findings and `make e2e` passes. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 4.2 Usability check: an embedder follows the SDK page `docs/reference/sdk.md` alone and starts a server with one store option. Check: the steps in the pull request. A person's step.

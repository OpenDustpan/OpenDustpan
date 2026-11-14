## 0. Triage
- [ ] 0.1 For each open PR that touches `pkg/store/`: merge, close as stale, or mark "port with branch mode". Check: a table of the 23 PRs and their outcome in STATE.md.
## 1. Prepare (PR B)
- [ ] 1.2 Add the branch mode to the script. Check: it ports one open store PR branch and `go build ./...` passes on it.
- [ ] 1.1 Write `scripts/move-store.sh` and run it on a fresh main. Check: `go build ./...` passes on the result, and `git diff --stat` shows only renames, import lines and generated files.
## 2. Constructors (PR A, can land first)
- [ ] 2.1 Add `dustpan.OpenStore`, `dustpan.BuiltinStore`, `dustpan.StorePorts` and `dustpan.WithBuiltinStore`; keep `dustpan.WithSQLite` and `dustpan.WithPostgreSQL`; make `cmd/opendustpan` use `OpenStore` and `WithBuiltinStore`. Check: the `pkg/dustpan` test of the proposal; `cmd/opendustpan` has no store wiring of its own.
## 3. Paths
- [ ] 3.1 Update `sqlc.yaml`, the Makefile, CI paths, `.golangci.yml`, the layout in `AGENTS.md`, the docs. Check: `sqlc diff` clean, `make docs-lint` exits 0.
## 4. Verify
- [ ] 4.0 The SHA-256 of each baseline is unchanged by the move. Check: a test or a script compares them.
- [ ] 4.1 `make check` with zero findings and `make e2e`. Check: output in the pull request.
- [ ] 4.2 Usability check: an embedder follows the SDK page and starts a server with one store option. Check: the steps in the pull request.

## 1. Fix the helpers

- [ ] 1.1 In `e2e/e2e_test.go:10115` (`replayScriptPath`), remove `runtime.Caller(0)` and return `filepath.Join("..", "scripts", "import-tfe-state.sh")`. Remove the `runtime` import if no other use remains. Check: `GOFLAGS=-trimpath go vet ./e2e/...` exits 0.
- [ ] 1.2 In `internal/zonegen/zonegen_test.go:17` (`committedPath`), remove `runtime.Caller(0)` and return `filepath.Join("..", "..", "pkg", "preferences", "zones_gen.go")`. Check: `GOFLAGS=-trimpath go test ./internal/zonegen/... -run TestCommittedListMatchesToolchain -count=1` passes.
- [ ] 1.3 In `internal/cli/reference_test.go:20` (`repoRoot`), remove `runtime.Caller(0)` and return `filepath.Join("..", "..")`. Check: `GOFLAGS=-trimpath go test ./internal/cli/... -run 'TestConfigurationReference' -count=1` passes, and `internal/cli/settings_test.go:36` still uses `repoRoot`.
- [ ] 1.4 In `internal/pulumijournal/internal/gen/gen_test.go:22` (`testDir`), remove `runtime.Caller(0)` and return `"."`. At :44, replace `filepath.Dir(filepath.Dir(testDir(t)))` with `filepath.Join(testDir(t), "..", "..")`, because `filepath.Dir(".")` is `"."`. Check: `GOFLAGS=-trimpath go test ./internal/pulumijournal/... -run TestGenerateMatchesCommittedFile -count=1` passes.

## 2. Definition of done

- [ ] 2.1 Run the trimpath check and the local checks. Check: `GOFLAGS=-trimpath go test ./internal/zonegen/... ./internal/cli/... ./internal/pulumijournal/...` passes, `rg -n "runtime.Caller\(0\)" --glob '*_test.go'` finds no line, and `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).

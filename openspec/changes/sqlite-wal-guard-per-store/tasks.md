## 1. Move the settings into the store

- [x] 1.1 Make the log-size settings (`walGuardInterval` at pkg/store/sqlite/sqlite.go:101, `walGuardThresholdBytes` at :109, `walGuardBackoffCap` at :122, `walGuardWarnAfter` at :190) and the tick collection (:147-161) unexported fields of `Store`, set by `Open` with the values of today, and read them from the store in the log-size check (pkg/store/sqlite/sqlite.go:1884-1923). The change moves settings that exist today and adds no new check, so it needs no new conformance test. Remove the package variables and `walGuardMu`. Check: `rg -n '^var walGuard' pkg/store/sqlite` finds no line and `go build ./pkg/store/sqlite/...` passes.
- [x] 1.2 Replace SetWALGuardForTest and `walHookMu` in `pkg/store/sqlite/export_internal_test.go` with an option of the test build that sets the log-size settings of one store, and make WALGuardTicksForTest read the ticks of one store. Change the callers in `pkg/store/sqlite/wal_test.go` (:54, :339, :688, :738, :786) to open their own store with the option. Check: `rg -n -i setwalguardfortest pkg` finds no line and `go test -timeout 60m -race ./pkg/store/sqlite/ -run WAL -count=1` passes.

## 2. Definition of done

- [ ] 2.1 Run the local checks (`Makefile`). Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).

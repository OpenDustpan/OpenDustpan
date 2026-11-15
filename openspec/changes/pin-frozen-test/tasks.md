## 1. Test first

- [ ] 1.1 Write the table test TestPinFrozen in the NEW file `internal/api/apidesc/pin_frozen_test.go` (package apidesc_test). Rows: `DocumentVersion` (`internal/api/apidesc/document.go:20`), info.version of the embedded description `internal/api/apidesc/openapi.json`, and the `version` line of `internal/api/apidesc/openapi.pin`; each is `0.36.0`. The failure text says: "The first release sets info.version and openapi.pin; until then they stay 0.36.0." Check: the test passes on main, and it fails when one value changes.

## 2. Target

- [ ] 2.1 Add the NEW target `pin-frozen` to the `Makefile`: it runs `go test ./internal/api/apidesc -run TestPinFrozen -count=1`. Add it to the target `check`. Check: `make pin-frozen` exits 0.

## 3. Definition of done

- [ ] 3.1 Run the local checks (`Makefile`). Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).

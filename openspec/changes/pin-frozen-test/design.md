## Context

Until the first server tag, the API description version stays at one fixed value while the description changes. The release sets the version and the pin together. A pull request that raises the version early passes every gate unless a test refuses it.

This change adds no server code, no route, no port, and no schema change. It adds a test in the existing package and a Makefile target.

## Decisions

1. **Fixed-value test in the existing package.** The NEW test `TestPinFrozen` in `internal/api/apidesc/pin_frozen_test.go` asserts three values equal `0.36.0`: `DocumentVersion` in `internal/api/apidesc/document.go`, `info.version` of the embedded description, and the `version` line of `internal/api/apidesc/openapi.pin`. The test reads only the files of its own package. It uses no git and takes no path input. The failure text names the one step that may change the values: the first tag.
2. **Makefile target runs the one test.** The NEW target `pin-frozen` runs `go test ./internal/api/apidesc -run TestPinFrozen -count=1`. The `check` target lists it. The `sha256` line of the pin is not checked because it changes with the description.

## Structure

No new server structure. The frozen value lives in the test constant `frozenVersion`, not in server code, so no caller can build a wrong version from it. No bug class is removed; the test holds the line until the first release removes it in the same pull request that sets both values.

No custom analyzer and no new tool. The check is a test in the existing package and a Makefile target.

## Risks

- The pull request of the first tag must set both values and remove this test together. If it sets the values without removing the test, the test fails.
- A reader may mistake `DocumentVersion` for a build version. The comment on the constant says it is the version of the description.

## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests first

- [ ] 1.1 Write failing golden and determinism tests in the NEW file internal/clientgen/python/python_test.go. Confirmed by: CI (clientgen python golden tests). Check: `go test ./internal/clientgen/python -run TestPythonGolden -v` with the NEW test `TestPythonGolden` fails on main
- [ ] 1.2 Write failing name tests in the NEW file internal/clientgen/python/names_test.go Check: `go test ./internal/clientgen/python -run TestPythonNames -v` with the NEW test `TestPythonNames` fails on main
- [ ] 1.3 Write failing transport tests in the NEW file internal/clientgen/conformance/python_test.go Check: `go test ./internal/clientgen/conformance -run TestPythonTransports -v` with the NEW test `TestPythonTransports` fails on main

## 2. implementation

- [ ] 2.1 Write the NEW protocol document docs/reference/clientgen-adapter-protocol.md before the adapter Check: `make docs-lint` passes
- [ ] 2.2 Implement the NEW package in internal/clientgen/python/python.go Check: `go test ./internal/clientgen/python -v` passes
- [ ] 2.3 Implement the NEW adapter in internal/clientgen/conformance/python_adapter.go Check: `go test ./internal/clientgen/conformance -run TestPython -v` with the NEW test `TestPython` passes
- [ ] 2.4 Fix list and map presence in the shared model in internal/clientgen/conformance/conformance.go:71 and update the Go and Python golden files. Confirmed by: CI (clientgen presence and golden tests). Check: `go test ./internal/clientgen/... -v` passes

## 3. definition of done

- [ ] 3.1 Add the wiring and add the new file docs/guides/python-client.md, run the usability check and run make check. A person's step. Check: `make check` with zero findings

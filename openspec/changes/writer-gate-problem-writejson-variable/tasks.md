## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Fix

- [ ] 1.1 Add failing gate cases in internal/api/problem/writer_gate_test.go:156 for WriteJSON with 500 and map body, variable and converted WriteHeader, and map or anonymous-struct encode. Check: `go test ./internal/api/problem -count=1` fails on main for these cases
- [ ] 1.2 Narrow the writer in internal/api/problem/problem.go:539 to the NEW WriteSuccess writer and the NEW WriteProblem writer and migrate each non-test caller such as internal/api/objectcontext/objectcontext.go:241 and internal/api/runnerapi/errors.go:285. Check: `go test ./internal/api/problem -run TestRefusedCodeLogsThroughFaultlog -count=1` passes
- [ ] 1.3 Pin the exception list in internal/api/problem/writer_gate_test.go:803 by sorted keys with exact set match and add a failing swap test. Check: `go test ./internal/api/problem -count=1` fails on main for a remove-plus-add swap
- [ ] 1.4 Extend BodyTypes in internal/errcode/errcodetest/bodies.go:24 for named map types and anonymous structs. Check: `go test ./internal/errcode/errcodetest -count=1` fails on main for map and anonymous-struct bodies

## 2. Definition of done

- [ ] 2.1 Run the definition of done: narrow writers wired, no user route, no e2e step, no docs page, usability check of the gate text. Confirmed by: CI (make check with zero findings). Check: `make check` with zero findings

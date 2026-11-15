## 1. Refine

- [ ] 1.1 Refine this change into a full change when its design is accepted. Write the refined text to NEW file openspec/changes/108-run-options-re-runs/design.md. Check: the change passes the factory checks.
- [ ] 1.2 Add the line of each NEW error code (the code and its arguments, as `TestNewCodesHaveTheirLine` prints it) in its place in the name order: `run.no-such-run` in `internal/errcode/testdata/catalog/run.txt`. No route: the test reads the catalog only. Check: `go test ./internal/errcode/ -run TestNewCodesHaveTheirLine -count=1` passes.

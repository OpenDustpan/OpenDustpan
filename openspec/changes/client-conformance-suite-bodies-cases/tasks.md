## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Work

- [ ] 1.1 Write bodies for the seven buildable cases in internal/clientgen/conformance/conformance.go:359. Check: `go test ./internal/clientgen/conformance -run TestCasesOfTheFirstSliceHaveABody` fails on main and passes after
- [ ] 1.2 Add the cursor loop with the MaxPages counter in internal/clientgen/conformance/goclient/files/main.go.txt:166 and internal/clientgen/conformance/tsclient/files/adapter.mjs:95. Check: `make clients-conformance` passes; a hostile test sends endless cursors and asserts stop at MaxPages with a truncation report
- [ ] 1.3 Fix kill-after-Wait, Pdeathsig, IsGenerated matching, stale-walk skip, and RPCError.Message limit in internal/clientgen/conformance/process.go:271, internal/clientgen/writer.go:209, and internal/clientgen/clientgen.go:121. Check: `go test ./internal/clientgen/...` passes with hostile tests for PID-wrap kill skip, header-quote file, .git skip, and CR and LF cap at 512 bytes
- [ ] 1.4 Freeze generator goldens on the NEW file internal/clientgen/testdata/description.json and keep live freshness in `make clients-check`. Confirmed by: CI (clients-check job). Check: `make clients-check` still fails on a scratch stale edit; the test comment lists every template branch. Confirmed by: CI (clients-check job).

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Run the full check suite with zero findings. Confirmed by: CI (make check job). Check: `make check` with zero findings

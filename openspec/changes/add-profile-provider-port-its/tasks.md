## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the RunProvider cases for Spec, Check, secret literal, Open, deadline, oversize answer, status table, non-JSON body, redirect and message mask. Check: `go test ./pkg/profile/provider/providertest/ -run TestRunProviderConformance -count=1` fails on main
- [ ] 1.2 Add RunRotator cases with a fake remote and goleak. Check: `go test ./pkg/profile/provider/providertest/ -run TestRunRotatorConformance -count=1` fails on main
- [ ] 1.3 Add table tests for dial refusals, rebinding, redirect and listed host:port and CIDR. Check: `go test ./pkg/egress/... -run TestDialGuardRefusals -count=1` fails on main
- [ ] 1.4 Add validHost, Fetcher and Checkout hostile tests. Check: `go test ./internal/source/... -run TestFetcherPinAndCheckout -count=1` fails on main

## 2. Wiring

- [ ] 2.1 Add the ports, Spec and the registry with duplicate refusal, and the depguard globs. Check: `go test ./pkg/profile/... -count=1` passes
- [ ] 2.2 Add the two settings through internal/cli/settings.go with refusals, an audit entry and the fresh step for a weaker list. Check: `GET /api/dustpan/v1/settings` shows both settings and a widening PATCH without a fresh step answers 403
- [ ] 2.3 Wire the egresshttp client with the limits and route the source Fetcher through the dial guard. Check: `go test -race ./pkg/profile/... ./pkg/egress/... ./internal/source/... -count=1` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Write the configuration reference and the provider port guide in the same pull request. Check: `make docs-lint` passes
- [ ] 4.2 Run an e2e step that starts the server and reads the two settings. Check: `make e2e` passes
- [ ] 4.3 Run make check with zero findings. Check: `make check` reports zero findings

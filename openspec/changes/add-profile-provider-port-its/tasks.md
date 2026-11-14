## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the RunProvider cases in pkg/profile/provider/providertest/providertest.go:71 (func RunProvider) for Spec, Check, secret literal, Open, deadline, oversize answer, status table, non-JSON body, redirect and message mask. Check: `go test ./pkg/profile/provider/providertest/ -run TestRunProviderConformance -count=1` fails on main
- [ ] 1.2 Add RunRotator (NEW) in NEW file pkg/profile/provider/providertest/rotator.go and its cases with a fake remote and goleak. Check: `go test ./pkg/profile/provider/providertest/ -run TestRunRotatorConformance -count=1` fails on main
- [ ] 1.3 Add table tests in NEW file pkg/egress/dialguard_refusals_test.go for dial refusals, rebinding, redirect and listed host:port and CIDR. Check: `go test ./pkg/egress/... -run TestDialGuardRefusals -count=1` fails on main
- [ ] 1.4 Add registry tests in pkg/profile/provider/registry_test.go for Register and Lookup, with a duplicate pair and an unknown pair. Check: `go test ./pkg/profile/... -run TestProviderRegistry -count=1` fails on main
- [ ] 1.5 Add validHost, Fetcher and Checkout hostile tests in NEW file internal/source/hostile_test.go (Checkout is at internal/source/source.go:263). Check: `go test ./internal/source/... -run TestFetcherPinAndCheckout -count=1` fails on main

## 2. Wiring

- [ ] 2.1 Add the ports (NEW) and Spec (NEW), and extend the existing file pkg/profile/provider/registry.go in place with duplicate refusal (NEW behavior), and add the depguard globs in the existing file .golangci.yml. Check: `go test ./pkg/profile/... -count=1` passes
- [ ] 2.2 Add the two settings through internal/cli/settings.go with refusals, an audit entry and the fresh step for a weaker list. Check: `GET /api/dustpan/v1/settings` shows both settings and a widening PATCH without a fresh step answers 403
- [ ] 2.3 Wire the egresshttp client with the limits in NEW file pkg/profile/provider/client.go and route the source Fetcher through the dial guard in internal/source/source.go. Check: `go test -race ./pkg/profile/... ./pkg/egress/... ./internal/source/... -count=1` passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Write the configuration reference for profileProviderNetworks and profileCACertificates in NEW file docs/reference/profile-provider-settings.md, and the provider port guide in NEW file docs/guides/profile-provider-port.md, in the same pull request. Check: `make docs-lint` passes
- [ ] 4.2 Run an e2e step that starts the real server, then calls `GET /api/dustpan/v1/settings` with an administrator access token and checks both settings. Check: `make e2e` passes with the settings step. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.3 Run make check with zero findings. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).

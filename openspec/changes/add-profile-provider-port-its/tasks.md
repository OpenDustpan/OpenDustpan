## 1. Failing tests first

- [ ] 1.1 Add RunProvider cases for Spec, Check, secret plain literal, Open, deadline, oversize answer, status table, redirect and message mask with a fake remote. Check: `go test ./pkg/profile/provider/providertest/ -run TestRunProviderConformance -count=1` fails on main and passes after the change.
- [ ] 1.2 Add RunRotator cases with a fake remote and goleak verification. Check: `go test ./pkg/profile/provider/providertest/ -run TestRunRotatorConformance -count=1` fails on main and passes after the change.
- [ ] 1.3 Add table tests that refuse link-local, loopback and unlisted private addresses, refuse rebinding and cross-host redirect with ErrUnavailable, and allow a listed host:port for its namespaces. Check: `go test ./internal/egress/... -run TestDialGuardRefusals -count=1` passes and `go test ./internal/egress/... -run TestNoCrossHostRedirect -count=1` passes.

## 2. Wiring

- [ ] 2.1 Add Provider, Rotator, TokenSource, Spec and the ErrRemoteDenied, ErrRemoteNotFound, ErrUnavailable and ErrTooLarge sentinels with the 256 KiB request cap and the 10 second default with 30 second maximum timeout. Check: `go test ./pkg/profile/... -count=1` passes.
- [ ] 2.2 Add the adapter registry with duplicate refusal at start, unknown pair refusal at save and open, and depguard globs for pkg/profile, profiletest and providertest. Check: `opendustpan serve` refuses a duplicate kind plus name with the pair in the message.
- [ ] 2.3 Add profileProviderNetworks and profileCACertificates through the setting declaration in internal/cli/settings.go with /0, link-local and loopback refusal and an audit entry for each change. Check: `grep -r profileProviderNetworks docs/reference/configuration.md` shows the setting without a hand edit.
- [ ] 2.4 Wire egresshttp Client with NoRedirect, HTTPSOnly, pinned dial guard, 1 MiB limit and at most 16 parallel calls per host per server process with wait inside the timeout. Check: `go test -race ./pkg/profile/... ./internal/egress/... -count=1` passes.

## 3. Definition of done

- [ ] 3.1 Run make check with zero findings. Check: `make check` reports zero findings with output pasted in the pull request.
- [ ] 3.2 Run openspec validation. Check: `npx @fission-ai/openspec@1.15.0 validate --all` passes with DISABLE_TELEMETRY=1 OPENSPEC_TELEMETRY=0 DO_NOT_TRACK=1.
- [ ] 3.3 Check the configuration reference and the provider port guide from the docs page alone. Check: The docs page command runs as written against a dev server.

## 0. Confirm against the code first

- [ ] 0.1 Read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.
- [ ] 0.2 Run the adversarial second pass of `openspec/project.md` with a model of another provider. Put each answer in proposal.md, answered or as an open question. Check: proposal.md lists the answers, and "Open questions" still reads "none", or the change stops.

## 1. Failing tests first

- [ ] 1.1 First boot stores the settings. Check: `go test ./pkg/store/... -run TestRecoverySettingsFirstBoot -count=1` fails on main (NEW test, both dialects).
- [ ] 1.2 A valid request makes one temporary administrator and an encrypted answer. Check: `go test ./internal/recovery/ -run TestSignedRequestMakesOneTempAdmin -count=1` fails on main (NEW test).
- [ ] 1.3 Eleven refusal reasons give one answer and eleven audit reasons. Check: `go test ./internal/api/server/ -run TestRecoveryRefusalsLookTheSame -count=1` fails on main (NEW test).
- [ ] 1.4 Replay, two at once on two instances, and a crash before the commit. Check: `go test ./pkg/store/... -run TestRecoveryNonceOnce -count=1` fails on main (NEW test).
- [ ] 1.5 The time window and the skew. Check: `go test ./internal/recovery/ -run TestRequestWindow -count=1` fails on main (NEW test).
- [ ] 1.6 The shared rate limit with the /64 group. Check: `go test ./pkg/store/... -run TestRecoveryRateLimitShared -count=1` fails on main (NEW test).
- [ ] 1.7 One live recovery. Check: `go test ./pkg/store/... -run TestRecoveryOneLive -count=1` fails on main (NEW test).
- [ ] 1.8 The review opens whatever the opt-in, and a failed notification makes no temporary administrator. Check: `go test ./internal/recovery/ -run TestRecoveryOpensReview -count=1` fails on main (NEW test).
- [ ] 1.9 The two recovery steps pass, and other fresh steps refuse. Check: `go test ./internal/api/server/ -run TestRecoveryAdminFreshSteps -count=1` fails on main (NEW test).
- [ ] 1.10 Key add, overlap, removal and the last key. Check: `go test ./internal/api/server/ -run TestRecoveryKeyRotation -count=1` fails on main (NEW test).
- [ ] 1.11 The signer refuses a plain key file. Check: `go test ./cmd/opendustpan/ -run TestRecoverySignNeedsPassphrase -count=1` fails on main (NEW test).
- [ ] 1.12 A hostile-input test for each layer of the design table, each with the other layers off. Check: `go test ./internal/recovery/ ./internal/api/server/ ./pkg/store/... -run 'TestRecoveryLayer' -count=1` fails on main (NEW tests).

## 2. Build

- [ ] 2.1 The four tables in the baseline of each dialect, in their places in the name order, and the store methods with their conformance suite. Check: `go test ./pkg/store/... -run TestRecovery -count=1 -timeout 60m` passes.
- [ ] 2.2 `breakglass.Use` gets `Mode` and `Source`, and `Use.Validate` checks them. Check: `go test ./pkg/breakglass/ -count=1` passes.
- [ ] 2.3 `internal/recovery`: `parseSignedRequest`, the transaction, HPKE. The depguard rules of the design. Check: `go test ./internal/recovery/ -count=1` and `golangci-lint run ./...` pass.
- [ ] 2.4 The routes with their actions, the public list entry, the error codes `recovery.refused` and `recovery.last-key` in the catalog. Check: the route test and `go test ./internal/api/server/ -count=1` pass.
- [ ] 2.5 The exception of ADR 372 decision 6 in the admin reset route and the enrollment code route. Check: `TestRecoveryAdminFreshSteps` passes.
- [ ] 2.6 The CLI commands `recovery request|sign|verify|submit` and `recovery keys list|add|remove`. Check: `go test ./cmd/opendustpan/ -run TestRecovery -count=1` passes.
- [ ] 2.7 The purge of nonces and attempt windows. Check: `go test ./pkg/store/... -run TestRecoveryPurge -count=1` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request.
- [ ] 3.2 Update the test of ADR 245 decision 12 to name `internal/recovery` as the one other caller. Check: the test passes and fails when a third package names the host issuer.
- [ ] 3.3 Update `docs/guides/host-recovery.md` (the stored mode) and `docs/reference/configuration.md` (`--recovery-public-key-file`). Check: `make docs-lint` passes.

## 4. Definition of done

- [ ] 4.1 An e2e step `SignedRequestRecovery` against the real binary over TLS: one recovery, one replay, one bad signature. Check: `make e2e` passes.
- [ ] 4.2 The docs page `docs/guides/signed-request-recovery.md`, with the risk statement of ADR 295 decision 2, the always-on notice, the two recovery steps and the crash case. Check: a test runs each command of the page.
- [ ] 4.3 The usability check from the docs page alone. Check: the operator recovers, and each error names one command.
- [ ] 4.4 Run all checks. Check: `make check` reports zero findings.

## 0. Confirm against the code first

- [ ] 0.1 Read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.
- [ ] 0.2 Run the adversarial second pass of `openspec/project.md` with a model of another provider. Put each answer in proposal.md, answered or as an open question. Check: proposal.md lists the answers, and "Open questions" still reads "none", or the change stops.

## 1. Failing tests first

- [ ] 1.1 First boot stores the settings. Check: `go test ./pkg/store/... -run TestRecoverySettingsFirstBoot -count=1` fails on main (NEW test, both dialects).
- [ ] 1.2 A valid request makes one temporary administrator and an encrypted answer. Check: `go test ./internal/recovery/ -run TestSignedRequestMakesOneTempAdmin -count=1` fails on main (NEW test).
- [ ] 1.3 Thirteen refusal reasons give one answer and thirteen audit reasons. Check: `go test ./internal/api/server/ -run TestRecoveryRefusalsLookTheSame -count=1` fails on main (NEW test).
- [ ] 1.4 Replay, two at once on two instances, and a crash before the commit. Check: `go test ./pkg/store/... -run TestRecoveryNonceOnce -count=1` fails on main (NEW test).
- [ ] 1.5 The time window and the skew. Check: `go test ./internal/recovery/ -run TestRequestWindow -count=1` fails on main (NEW test).
- [ ] 1.6 The shared rate limit with the /64 group. Check: `go test ./pkg/store/... -run TestRecoveryRateLimitShared -count=1` fails on main (NEW test).
- [ ] 1.7 One live recovery. Check: `go test ./pkg/store/... -run TestRecoveryOneLive -count=1` fails on main (NEW test).
- [ ] 1.8 The review opens whatever the opt-in, and a failed notification makes no temporary administrator. Check: `go test ./internal/recovery/ -run TestRecoveryOpensReview -count=1` fails on main (NEW test).
- [ ] 1.9 The two recovery steps pass, and other fresh steps refuse. Check: `go test ./internal/api/server/ -run TestRecoveryAdminFreshSteps -count=1` fails on main (NEW test).
- [ ] 1.10 Key add, overlap, removal and the last key. Check: `go test ./internal/api/server/ -run TestRecoveryKeyRotation -count=1` fails on main (NEW test).
- [ ] 1.11 The signer refuses a plain key file. Check: `go test ./cmd/opendustpan/ -run TestRecoverySignNeedsPassphrase -count=1` fails on main (NEW test).
- [ ] 1.12 A hostile-input test for each layer of the design table, each with the other layers off. Check: `go test ./internal/recovery/ ./internal/api/server/ ./pkg/store/... -run 'TestRecoveryLayer' -count=1` fails on main (NEW tests).
- [ ] 1.13 The rate limit bounds the signature check: many requests with bad signatures from one source stop at the limit, and the signature check does not run for a refused request. Check: `go test ./internal/recovery/ -run TestRecoveryRateLimitBoundsSignature -count=1` fails on main (NEW test).
- [ ] 1.14 A key file path that leaves the root and a key file larger than 16 KiB are refused. Check: `go test ./cmd/opendustpan/ -run TestRecoveryKeyFileContained -count=1` fails on main (NEW test).
- [ ] 1.15 A private key file that group or others can read is refused. Check: `go test ./cmd/opendustpan/ -run TestRecoverySignPrivateKeyMode -count=1` fails on main (NEW test).
- [ ] 1.16 A `person` member outside the allowlist is refused. Check: `go test ./internal/recovery/ -run TestRecoveryPersonAllowlist -count=1` fails on main (NEW test).
- [ ] 1.17 A hostile forwarded header has no effect on the source, and a malformed address is refused. Check: `go test ./internal/recovery/ ./internal/api/server/ -run TestRecoverySourceAddress -count=1` fails on main (NEW test).
- [ ] 1.18 A request in maintenance mode gets the one answer, and the audit reason is `maintenance`. Check: `go test ./internal/api/server/ -run TestRecoveryMaintenanceRefused -count=1` fails on main (NEW test).
- [ ] 1.19 A request whose `notAfter` is more than 10 minutes after `notBefore` is refused. Check: `go test ./internal/recovery/ -run TestRecoveryRequestWindowTenMinutes -count=1` fails on main (NEW test).
- [ ] 1.20 A malformed or short nonce is refused. Check: `go test ./internal/recovery/ -run TestRecoveryNonceShape -count=1` fails on main (NEW test).
- [ ] 1.21 A key in a request that is not `ssh-ed25519` is refused. Check: `go test ./internal/recovery/ -run TestRecoveryKeyTypeEd25519 -count=1` fails on main (NEW test).
- [ ] 1.22 A request with a wrong content type is refused, and a hostile forwarded address has no effect. Check: `go test ./internal/api/server/ -run TestRecoveryContentTypeAndForwarded -count=1` fails on main (NEW test).
- [ ] 1.23 The successful use writes one audit entry that holds the mode, the time and the client address and the action `recovery:use-signed-request`, in the same transaction. Check: `go test ./internal/recovery/ -run TestRecoveryUseAuditEntry -count=1` fails on main (NEW test).
- [ ] 1.24 A malformed `answerKey` (a key of 31 bytes, a key of 33 bytes, a key that is not base64) is refused, and a failed seal of the answer refuses the request and creates nothing. Check: `go test ./internal/recovery/ -run TestRecoveryAnswerKeyAndSeal -count=1` fails on main (NEW test).
- [ ] 1.25 A `reason` with a Unicode Cc or Cf character, and a `reason` of more than 500 characters, are each refused. Check: `go test ./internal/recovery/ -run TestRecoveryReason -count=1` fails on main (NEW test).
- [ ] 1.26 A request with a wrong audience is refused at the service layer. Check: `go test ./internal/recovery/ -run TestRecoveryAudience -count=1` fails on main (NEW test).
- [ ] 1.27 A `version` member other than 1 is refused. Check: `go test ./internal/recovery/ -run TestRecoveryVersion -count=1` fails on main (NEW test).
- [ ] 1.28 A request whose installation name matches but whose audience does not is refused. Check: `go test ./internal/recovery/ -run TestRecoveryInstallationNameAndAudience -count=1` fails on main (NEW test).
- [ ] 1.29 Every file that the CLI reads or writes in the flow (`request.json`, `request.json.sig`, `request.key` and the key files) refuses a path that leaves the root. Check: `go test ./cmd/opendustpan/ -run TestRecoveryFlowFileContained -count=1` fails on main (NEW test).
- [ ] 1.30 Every such file larger than 16 KiB is refused. Check: `go test ./cmd/opendustpan/ -run TestRecoveryFlowFileSize -count=1` fails on main (NEW test).
- [ ] 1.31 `request.key` with mode 0644 is refused when the CLI reads it. Check: `go test ./cmd/opendustpan/ -run TestRecoveryRequestKeyMode -count=1` fails on main (NEW test).
- [ ] 1.32 A first-boot key file that holds an `ssh-rsa` key is refused. Check: `go test ./cmd/opendustpan/ -run TestRecoveryFirstBootKeyType -count=1` fails on main (NEW test). `serve` does not start and the store holds no mode and no key.
- [ ] 1.33 A first-boot key file that holds two `ssh-ed25519` keys is refused. Check: `go test ./cmd/opendustpan/ -run TestRecoveryFirstBootKeyTwoKeys -count=1` fails on main (NEW test). `serve` does not start and the store holds no mode and no key.
- [ ] 1.34 A first-boot key file that does not parse is refused. Check: `go test ./cmd/opendustpan/ -run TestRecoveryFirstBootKeyUnparsable -count=1` fails on main (NEW test). `serve` does not start and the store holds no mode and no key.
- [ ] 1.35 An empty first-boot key file is refused. Check: `go test ./cmd/opendustpan/ -run TestRecoveryFirstBootKeyEmpty -count=1` fails on main (NEW test). `serve` does not start and the store holds no mode and no key.

## 2. Build

- [ ] 2.1 The four tables in the baseline of each dialect, in their places in the name order, and the store methods with their conformance suite. Check: `go test ./pkg/store/... -run TestRecovery -count=1 -timeout 60m` passes.
- [ ] 2.2 `breakglass.Use` gets `Mode` and `Source`, and `Use.Validate` checks them. Check: `go test ./pkg/breakglass/ -count=1` passes.
- [ ] 2.3 `internal/recovery`: `parseSignedRequest`, the transaction, HPKE. The depguard rules of the design. Check: `go test ./internal/recovery/ -count=1` and `golangci-lint run ./...` pass.
- [ ] 2.4 The routes with their actions, the public list entry, the error codes `recovery.refused` and `recovery.last-key` in the catalog. Check: the route test and `go test ./internal/api/server/ -count=1` pass.
- [ ] 2.5 The exception of ADR 372 decision 6 in the admin reset route and the enrollment code route. Check: `TestRecoveryAdminFreshSteps` passes.
- [ ] 2.6 The CLI commands `recovery request|sign|verify|submit` and `recovery keys list|add|remove`. Check: `go test ./cmd/opendustpan/ -run TestRecovery -count=1` passes.
- [ ] 2.7 The purge of nonces and attempt windows. Check: `go test ./pkg/store/... -run TestRecoveryPurge -count=1` passes.
- [ ] 2.8 The review list filters on the mode. Check: `go test ./internal/api/server/ -run TestRecoveryReviewListMode -count=1` passes.
- [ ] 2.9 The route test lists `POST /api/dustpan/v1/recovery/signed-requests` on its public list, with the action `recovery:use-signed-request` and the reason that the caller has no identity yet and the signed request is the credential. Check: the route test passes, and the refusal audit entry and the break-glass use record that action.
- [ ] 2.10 The handler checks the stored recovery mode before any other work, with a defense-in-depth comment that names the store-transaction check. The store transaction keeps its own mode check. Check: the layer test fails when the handler check is removed.
- [ ] 2.11 Each check that runs at two layers has a defense-in-depth comment that names the other layer, and a test that sends hostile input directly to that layer. Check: the layer tests pass, and each comment names the other layer.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request.
- [ ] 3.2 Update the test of ADR 245 decision 12 to name `internal/recovery` as the one other caller. Check: the test passes and fails when a third package names the host issuer.
- [ ] 3.3 Update `docs/guides/host-recovery.md` (the stored mode) and `docs/reference/configuration.md` (`--recovery-public-key-file`). Check: `make docs-lint` passes.

## 4. Definition of done

- [ ] 4.1 An e2e step `SignedRequestRecovery` against the real binary over TLS: one recovery, one replay, one bad signature. Check: `make e2e` passes.
- [ ] 4.2 The docs page `docs/guides/signed-request-recovery.md`, with the risk statement of ADR 295 decision 2, the always-on notice, the two recovery steps and the crash case. Check: a test runs each command of the page.
- [ ] 4.3 The usability check from the docs page alone. Check: the operator recovers, and each error names one command.
- [ ] 4.4 Run all checks. Check: `make check` reports zero findings.

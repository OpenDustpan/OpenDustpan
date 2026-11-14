## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing test TestLogSinkBatchVerifiesWithSharedVerify in NEW file `pkg/webhooksig/sign_test.go` for shared verify of a log-sink batch. Confirmed by: CI (`make lint`) Check: NEW file `pkg/webhooksig/sign_test.go` TestLogSinkBatchVerifiesWithSharedVerify fails on main, passes after.
- [ ] 1.2 Add failing test TestOldLogSinkFormRefused in NEW file `pkg/webhooksig/sign_test.go` for refusal of the old timestamp-body form in `pkg/logsink/webhook/webhook.go`. Confirmed by: CI (`make lint`) Check: NEW file `pkg/webhooksig/sign_test.go` TestOldLogSinkFormRefused fails on main, passes after.
- [ ] 1.3 Add failing test TestUnknownSinkSecretRefused in NEW file `pkg/webhooksig/sign_test.go` for refusal of unknown secrets during rotation in `pkg/notify/sign.go`. Confirmed by: CI (`make lint`) Check: NEW file `pkg/webhooksig/sign_test.go` TestUnknownSinkSecretRefused fails on main, passes after.

## 2. Build

- [ ] 2.1 Move Sign, Verify, header names, tolerance, and secret cap from `pkg/notify/sign.go` to NEW leaf package `pkg/webhooksig` with stdlib imports only and keep aliases in `pkg/notify/sign.go`. Confirmed by: CI (`make lint`) Check: rg webhooksig.Sign shows callers in `pkg/notify/sign.go` and `pkg/logsink/webhook/webhook.go`
- [ ] 2.2 Make `pkg/logsink/webhook/webhook.go` sign each batch with the shared call, send a generated batch delivery ID in Dustpan-Notification-Id, and accept one or two secrets. Confirmed by: CI (`make lint`) Check: `make lint` with zero findings
- [ ] 2.3 Add the NEW package `pkg/webhooksig` to the depguard domain list with no other import change. Confirmed by: CI (`make lint`) Check: depguard config lists the NEW package in the domain list

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Update the receiver guide in `pkg/logsink/webhook/webhook.go` docs comment, run the receiver verification path, run usability check, run `make check` with zero findings. A person's step. Check: Operator completes the receiver guide path from the docs page alone.

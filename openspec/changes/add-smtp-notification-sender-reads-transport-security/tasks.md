## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Transport

- [ ] 1.1 Add failing transport tests in NEW file pkg/notify/smtp/transport_test.go for plain refused with opt-in off, bad certificate refused, downgrade refused, and no credential on plain. No route: the test dials a fake relay only. Check: TestSmtpPlainRefusedWhenOptInOff in NEW file pkg/notify/smtp/transport_test.go fails on main, passes after. No route: the test dials a fake relay only.
- [ ] 1.2 Add the NEW sender in NEW file pkg/notify/smtp/transport.go with TLS by default, per-dial setting read, loopback peer-IP exception, and stable refusal codes. No route: the test flips the fake setting between dials only. Check: TestSmtpReadsPlainSettingEachDial in NEW file pkg/notify/smtp/transport_test.go fails on main, passes after. No route: the test flips the fake setting between dials only.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Wire the sender behind ANY/GET/PATCH /api/dustpan/v1/settings with an access token, add the transport guide section in docs, add the e2e step against a test SMTP server in NEW file pkg/notify/smtp/transport.go and NEW file pkg/notify/smtp/transport_test.go. Confirmed by: CI (make check and the e2e step). Check: PATCH ANY/GET/PATCH /api/dustpan/v1/settings with an access token plus `make check` with zero findings. Confirmed by: CI (make check and the e2e step).

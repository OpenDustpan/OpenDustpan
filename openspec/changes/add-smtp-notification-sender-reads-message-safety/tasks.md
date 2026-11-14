## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Build

- [ ] 1.1 Add failing tests for header refusal, recipient shape, text-only body, UTF-8 subject, send bounds, timeout, fixed sender, failure mapping, redaction, and refusal record in NEW file pkg/notify/smtp/message_test.go. Confirmed by: CI (go test ./pkg/notify/smtp -run TestSafeHeadersRefuseInjection). Check: NEW test TestSafeHeadersRefuseInjection in NEW file pkg/notify/smtp/message_test.go fails on main and passes after
- [ ] 1.2 Build the NEW message constructor and the NEW send path with 50 recipient and 1 MB bounds in NEW file pkg/notify/smtp/sender.go. Confirmed by: CI (go test ./pkg/notify/smtp -run TestSendBoundsRefuseBeforeDial). Check: NEW test TestSendBoundsRefuseBeforeDial in NEW file pkg/notify/smtp/sender_test.go passes with no relay dial

## 2. Definition of done

- [ ] 2.1 Wire the safe send path to notification sends with errors that name the one check in NEW file pkg/notify/smtp/sender.go. Confirmed by: CI (go test ./pkg/notify/smtp -run TestSafeSendPathUsed). Check: NEW test TestSafeSendPathUsed in NEW file pkg/notify/smtp/sender_test.go passes through the safe send path
- [ ] 2.2 Add the docs page for safe content and failure errors in NEW file docs/guides/smtp-message-safety.md. Confirmed by: CI (docs-lint and link check). Check: docs page command runs as written against a dev server
- [ ] 2.3 Run usability from the docs in NEW file docs/guides/smtp-message-safety.md and `make check` with zero findings. A person's step. Check: `make check` reports zero findings

## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the failing table test in NEW internal/textcheck/textcheck_test.go for Cc, Cf with U+200B, Zl with U+2028 and Zp with U+2029 Check: go test ./internal/textcheck -run TestIsHiddenRuneTable -v

## 2. Fix

- [ ] 2.1 Replace copied checks with NEW internal/textcheck in internal/api/pulumi/stack_page.go, pkg/inventory/query.go, internal/unmanaged/finding.go, internal/unitimport/validate.go, pkg/history/redaction.go and internal/issuersvc/rotate.go Check: `rg -n textcheck internal/api/pulumi/stack_page.go pkg/inventory/query.go internal/unmanaged/finding.go internal/unitimport/validate.go pkg/history/redaction.go internal/issuersvc/rotate.go`
- [ ] 2.2 Replace copied checks with NEW internal/textcheck in pkg/drift/drift.go, pkg/accesstoken/accesstoken.go, pkg/gate/approvers_check.go, pkg/principal/principal.go, pkg/backup/devsubject.go, pkg/trustrule/trustrule.go and internal/api/refusal/clean.go Check: `rg -n textcheck pkg/drift/drift.go pkg/accesstoken/accesstoken.go pkg/gate/approvers_check.go pkg/principal/principal.go pkg/backup/devsubject.go pkg/trustrule/trustrule.go internal/api/refusal/clean.go`
- [ ] 2.3 Point pkg/notify/chat/chat.go at NEW internal/textcheck and keep the one documented U+000A exemption Check: go test ./pkg/notify/chat -run TestChatHiddenRune -v
- [ ] 2.4 Point existing callers in pkg/job/job.go, pkg/tempadmin/tempadmin.go, pkg/manuallock/manuallock.go, pkg/savedview/service.go and internal/trustrulesvc/exchange.go at NEW internal/textcheck Check: `rg -n -F 'audit.IsHiddenRune' pkg internal` shows no caller outside pkg/audit/audit.go
- [ ] 2.5 Add guard test in NEW internal/textcheck/guard_test.go that fails on a new direct unicode.IsControl or unicode.Is category check outside NEW internal/textcheck with an exemption list Check: go test ./internal/textcheck -run TestNoNewDirectCheck -v

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run the affected unit tests for NEW internal/textcheck, pkg/audit/audit.go, pkg/job/job.go and pkg/notify/chat/chat.go Check: go test ./internal/textcheck ./pkg/audit ./pkg/job ./pkg/notify/chat -count=1
- [ ] 4.2 Run the refusal step against the real server through the write path of internal/api/pulumi/stack_page.go with an access token on an existing write route Check: the API call with U+2028 in internal/api/pulumi/stack_page.go answers invalid input and names the field
- [ ] 4.3 Update the docs page that names NEW internal/textcheck and run the usability check. A person's step. Check: docs page commands run as written for NEW internal/textcheck
- [ ] 4.4 Run make check with zero findings for the NEW internal/textcheck change. Confirmed by: CI (make check). Check: make check with zero findings

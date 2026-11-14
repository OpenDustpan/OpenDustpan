---
version: 1
---

Issue: #2404.

## Problem

Duplicated input checks refuse different character sets, so a value that one path accepts can carry U+200B or U+2028 into a log, an audit entry, a chat message or a console view.

## Evidence

- The shared set lives in pkg/audit as IsHiddenRune. Proof: pkg/audit/audit.go:195 (func IsHiddenRune)
- Direct unicode.IsControl checks remain across the tree. Proof: rg -n -F --glob '!*_test.go' 'unicode.IsControl' pkg internal cmd matches 47 lines, first lines include pkg/backup/devsubject.go:22 and internal/unmanaged/finding.go:301
- audit.IsHiddenRune is called from several domains. Proof: rg -n -F --glob '!*_test.go' 'audit.IsHiddenRune' pkg internal cmd matches 19 lines, first lines include pkg/tempadmin/tempadmin.go:103 and pkg/manuallock/manuallock.go:194

## Fix boundary

- Add the NEW package internal/textcheck with func IsHiddenRune covering Cc, Cf, Zl and Zp
- Replace the copied checks with the shared set at internal/api/pulumi/stack_page.go, pkg/inventory/query.go, internal/unmanaged/finding.go, internal/unitimport/validate.go, pkg/history/redaction.go, internal/issuersvc/rotate.go, pkg/drift/drift.go, pkg/accesstoken/accesstoken.go, pkg/gate/approvers_check.go, pkg/principal/principal.go, pkg/backup/devsubject.go, internal/api/refusal/clean.go, pkg/trustrule/trustrule.go and pkg/notify/chat/chat.go with its one documented U+000A exemption
- Point the existing audit.IsHiddenRune callers at the NEW function and keep audit.IsHiddenRune as a wrapper in pkg/audit
- Add one table test plus a guard check against new direct unicode.IsControl checks outside the leaf

Out of scope:

- Extension of the hidden-rune set beyond Cc, Cf, Zl and Zp belongs to qa-round-10-minor-findings, which extends the same set later
- Chat rendering rules beyond the one line-feed exemption

## Check

- Add the failing table test in NEW internal/textcheck/textcheck_test.go for Cc, Cf with U+200B, Zl with U+2028 and Zp with U+2029 Check: go test ./internal/textcheck -run TestIsHiddenRuneTable -v
- Replace copied checks with NEW internal/textcheck in internal/api/pulumi/stack_page.go, pkg/inventory/query.go, internal/unmanaged/finding.go, internal/unitimport/validate.go, pkg/history/redaction.go and internal/issuersvc/rotate.go Check: `rg -n textcheck internal/api/pulumi/stack_page.go pkg/inventory/query.go internal/unmanaged/finding.go internal/unitimport/validate.go pkg/history/redaction.go internal/issuersvc/rotate.go`
- Replace copied checks with NEW internal/textcheck in pkg/drift/drift.go, pkg/accesstoken/accesstoken.go, pkg/gate/approvers_check.go, pkg/principal/principal.go, pkg/backup/devsubject.go, pkg/trustrule/trustrule.go and internal/api/refusal/clean.go Check: `rg -n textcheck pkg/drift/drift.go pkg/accesstoken/accesstoken.go pkg/gate/approvers_check.go pkg/principal/principal.go pkg/backup/devsubject.go pkg/trustrule/trustrule.go internal/api/refusal/clean.go`
- Point pkg/notify/chat/chat.go at NEW internal/textcheck and keep the one documented U+000A exemption Check: go test ./pkg/notify/chat -run TestChatHiddenRune -v
- Point existing callers in pkg/job/job.go, pkg/tempadmin/tempadmin.go, pkg/manuallock/manuallock.go, pkg/savedview/service.go and internal/trustrulesvc/exchange.go at NEW internal/textcheck Check: `rg -n -F 'audit.IsHiddenRune' pkg internal` shows no caller outside pkg/audit/audit.go
- Add guard test in NEW internal/textcheck/guard_test.go that fails on a new direct unicode.IsControl or unicode.Is category check outside NEW internal/textcheck with an exemption list Check: go test ./internal/textcheck -run TestNoNewDirectCheck -v
- Run the affected unit tests for NEW internal/textcheck, pkg/audit/audit.go, pkg/job/job.go and pkg/notify/chat/chat.go Check: go test ./internal/textcheck ./pkg/audit ./pkg/job ./pkg/notify/chat -count=1
- Run the refusal step against the real server through the write path of internal/api/pulumi/stack_page.go with an access token on an existing write route Check: the API call with U+2028 in internal/api/pulumi/stack_page.go answers invalid input and names the field

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main defines the shared hidden-rune check. (`pkg/audit/audit.go:195 (func IsHiddenRune)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Where should the shared hidden-rune set live? No ADR in the pack touches hidden-character checks. The issue proposes a small tool-neutral leaf package. The change uses NEW internal/textcheck so no public API is added.
- Should every copy use the same set? The issue answers it: use Cc, Cf, Zl and Zp everywhere. Keep one narrower rule only in pkg/notify/chat/chat.go where U+000A stays allowed, with a comment that states the reason.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

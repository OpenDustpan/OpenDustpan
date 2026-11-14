---
version: 1
---

## Problem

An operator sends notices through the installation SMTP relay and needs each message safe to send: no header injection, no remote content, and a clear error when the relay refuses credentials or a recipient.

## Evidence

- Main catalogs settings errors including value-invalid. Proof: internal/errcode/catalog_settings.go:21
- Main has a header injection test in the proxy injector. Proof: pkg/launcher/proxy/inject_test.go:48

## Fix boundary

- Refuse header injection in addresses and subject and send nothing on refusal
- Encode notification mail as plain text with no remote content
- Enforce per-send recipient count and message size bounds before dial
- Map credential and recipient relay failures to clear errors with no secret in text

Out of scope:

- SMTP transport rules and plain-connection opt-in
- SMTP settings visibility, password source, and connection check

## Check

- Add failing tests for header refusal, recipient shape, text-only body, UTF-8 subject, send bounds, timeout, fixed sender, failure mapping, redaction, and refusal record in NEW file pkg/notify/smtp/message_test.go. Confirmed by: CI (go test ./pkg/notify/smtp -run TestSafeHeadersRefuseInjection). Check: NEW test TestSafeHeadersRefuseInjection in NEW file pkg/notify/smtp/message_test.go fails on main and passes after
- Build the NEW message constructor and the NEW send path with 50 recipient and 1 MB bounds in NEW file pkg/notify/smtp/sender.go. Confirmed by: CI (go test ./pkg/notify/smtp -run TestSendBoundsRefuseBeforeDial). Check: NEW test TestSendBoundsRefuseBeforeDial in NEW file pkg/notify/smtp/sender_test.go passes with no relay dial
- Wire the safe send path to notification sends with errors that name the one check in NEW file pkg/notify/smtp/sender.go. Confirmed by: CI (go test ./pkg/notify/smtp -run TestSafeSendPathUsed). Check: NEW test TestSafeSendPathUsed in NEW file pkg/notify/smtp/sender_test.go passes through the safe send path
- Add the docs page for safe content and failure errors in NEW file docs/guides/smtp-message-safety.md. Confirmed by: CI (docs-lint and link check). Check: docs page command runs as written against a dev server
- Run usability from the docs in NEW file docs/guides/smtp-message-safety.md and `make check` with zero findings. A person's step. Check: `make check` reports zero findings

Depends on:

- add-smtp-notification-sender-reads-transport-security
- `add-smtp-notification-sender-reads-transport-security` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main tests header injection handling in the proxy injector. (`pkg/launcher/proxy/inject_test.go:48 (TestInjectionHeader)`)
- Main warns once when a request log header is missing. (`pkg/dustpan/client_address_test.go:396 (TestRequestLogMissingHeaderWarnsOnce)`)
- Main tests credential load success for signed requests. (`pkg/blob/s3/sigv4_test.go:365 (TestLoadCredentialsSuccess)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (not owner-tagged).

- Decisions that apply: 2 (extended).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5; ADR 373 extends decision 1. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365, 373.
- Quote: "SMTP settings are installation settings. The relay, the sender and the credentials of SMTP are installation settings with the precedence of ADR 63 decision 1."
- Effect: Message safety keeps the relay, the sender, and the credentials as installation settings and keeps the password out of the database.
- Conflict: none.

**ADR 288: The sign-in page looks the same on every installation** (owner-tagged (all decisions)).

- Decisions that apply: 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The email rule stays. Emails show the product name as text and load no image (ADR 235 decision 11)."
- Effect: Message body follows the email rule and loads no remote content.
- Conflict: none.

**ADR 292: Plain LDAP and SMTP connections are refused unless an operator opts in** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Refused by default (owner). The server refuses an LDAP connection and an SMTP connection that has no TLS, except to a loopback address. (owner)"
- Quote: "Fail closed. If the setting is missing or not valid, the value is off. A refusal names the address, the setting to change and the one command or call that changes it."
- Effect: Message safety leaves transport refusal and fail-closed missing-setting behavior to the transport part and sends only over the relay the transport part allows.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where does the SMTP password come from? ADR 212 decision 2 answers it: the password comes only from an environment variable or from a file that a *_FILE key names, never from a flag or from the database.
- May notification mail load remote images? ADR 288 decision 5 answers it: text only with no image.
- What recipient count and message size bound does each send enforce? AWS SES practice caps recipients per send and message size with per-recipient status; this change fixes at most 50 recipients and at most 1 MB per send so the spec is closed with no open question.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

---
version: 1
---

Issue: #2945.

## Problem

An Operator configures email delivery for the installation. The server has no SMTP sender, so no mail leaves the installation. This part adds the transport that uses TLS by default and refuses plain connections unless the operator opts in.

## Evidence

- The installation has one settings resource with GET and PATCH at /api/dustpan/v1/settings. Proof: Route ANY/GET/PATCH /api/dustpan/v1/settings where route table or API description
- The installation settings default for plain connections is off. Proof: TestAllowPlainConnectionsDefaultsOff where pkg/egress/plainconn/plainconn_test.go:57
- A plain-setting path is expected to keep certificate checks. Proof: TestPlainSettingKeepsTheCertificateChecks where internal/ldapauth/plain_setting_test.go:117
- Sinks route through the built-in sender on main. Proof: TestSinkRoutesWithTheBuiltInSender where pkg/dustpan/sinks_test.go:205

## Fix boundary

- SMTP sender transport with TLS by default and STARTTLS in NEW file pkg/notify/smtp/transport.go
- Per-connection read of the plain-connections setting
- Loopback exception, fail-closed refusals, and stable refusal codes
- Hostile tests for plain-off refusal and bad certificates with setting on and off in NEW file pkg/notify/smtp/transport_test.go

Out of scope:

- Message safety and send failure handling
- Settings visibility, password source, and connection check

## Check

- Add failing transport tests in NEW file pkg/notify/smtp/transport_test.go for plain refused with opt-in off, bad certificate refused, downgrade refused, and no credential on plain. No route: the test dials a fake relay only. Check: TestSmtpPlainRefusedWhenOptInOff in NEW file pkg/notify/smtp/transport_test.go fails on main, passes after. No route: the test dials a fake relay only.
- Add the NEW sender in NEW file pkg/notify/smtp/transport.go with TLS by default, per-dial setting read, loopback peer-IP exception, and stable refusal codes. No route: the test flips the fake setting between dials only. Check: TestSmtpReadsPlainSettingEachDial in NEW file pkg/notify/smtp/transport_test.go fails on main, passes after. No route: the test flips the fake setting between dials only.
- Wire the sender behind ANY/GET/PATCH /api/dustpan/v1/settings with an access token, add the transport guide section in docs, add the e2e step against a test SMTP server in NEW file pkg/notify/smtp/transport.go and NEW file pkg/notify/smtp/transport_test.go. Confirmed by: CI (make check and the e2e step). Check: PATCH ANY/GET/PATCH /api/dustpan/v1/settings with an access token plus `make check` with zero findings. Confirmed by: CI (make check and the e2e step).

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main routes sinks with the built-in sender. (`pkg/dustpan/sinks_test.go:205 (TestSinkRoutesWithTheBuiltInSender)`)
- Main defaults plain connections to off. (`pkg/egress/plainconn/plainconn_test.go:57 (TestAllowPlainConnectionsDefaultsOff)`)
- Main keeps certificate checks when the plain setting is on in the LDAP path. (`internal/ldapauth/plain_setting_test.go:117 (TestPlainSettingKeepsTheCertificateChecks)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (not owner-tagged).

- Decisions that apply: 2 (extended).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5; ADR 373 extends decision 1. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365, 373.
- Quote: "SMTP settings are installation settings."
- Quote: "It comes only from an environment variable or from a file that a `*_FILE` key names (ADR 63 decision 4)."
- Effect: The transport uses the installation SMTP settings of decision 2 and does not change the password source rule.
- Conflict: none.

**ADR 292: Plain LDAP and SMTP connections are refused unless an operator opts in** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current), 5 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Refused by default (owner). The server refuses an LDAP connection and an SMTP connection that has no TLS, except to a loopback address. (owner)"
- Quote: "An explicit opt-in (owner). An installation can allow plain connections with one setting. The setting is off by default. (owner)"
- Quote: "Fail closed. If the setting is missing or not valid, the value is off. A refusal names the address, the setting to change and the one command or call that changes it."
- Quote: "TLS checks stay. A connection with TLS keeps the certificate checks of RFC 21. The opt-in does not turn off a certificate check."
- Effect: The sender refuses plain SMTP by default, reads the opt-in at each dial, fails closed with a stable code that names address plus setting plus call, and keeps certificate checks.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where do the SMTP host, port, sender, and credential come from? ADR 212 decision 2 says the relay, sender, and credentials are installation settings and the password comes only from env or file. This part uses those settings and does not change them.
- When is a plain SMTP connection allowed and what does a refusal say? ADR 292 decisions 1, 2, 5, and 6 answer it: refuse plain except loopback, one off-by-default opt-in read at each dial, fail closed with address plus setting plus call, TLS checks stay.
- Which TLS modes are supported and what happens when STARTTLS is stripped? Both implicit TLS on connect and STARTTLS on the plain port are supported, and STARTTLS is required before any credential. A stripped STARTTLS advertisement is a downgrade and is refused.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

---
version: 1
---

## Problem

Operator configures mail delivery for the installation and must see each SMTP value and where it comes from, keep the password out of the database, and prove the relay answers before users depend on it.

## Evidence

- One installation settings resource exists for GET and PATCH Proof: ANY/GET/PATCH /api/dustpan/v1/settings
- Settings error codes exist for unknown key, locked, and patch-invalid Proof: settings.key-unknown in internal/errcode/catalog_settings.go:7; settings.locked in internal/errcode/catalog_settings.go:9; settings.patch-invalid in internal/errcode/catalog_settings.go:12

## Fix boundary

- Declare SMTP relay, sender, and plain-connection opt-in as installation settings with effective value and source in GET
- Keep SMTP password out of database with env-or-file source only and 409 on PATCH
- Refuse plain-connection opt-in on hosted service with fail-closed off
- Add SMTP connection check that reports reachability and writes an audit entry
- Add guide page and e2e step against a test server

Out of scope:

- SMTP transport TLS refusal rules
- SMTP message safety and send failure handling
- SMTP envelope content and headers

## Check

- Add failing server test for GET settings SMTP keys with value and source in NEW internal/settingsvc/settings_test.go Check: TestSmtpSettingsShowEffectiveValueAndSource in NEW internal/settingsvc/settings_test.go calls GET /api/dustpan/v1/settings with access token and fails on main
- Add failing server test for PATCH SMTP password refused with 409 in NEW internal/settingsvc/settings_test.go Check: TestSmtpPasswordPatchConflict in NEW internal/settingsvc/settings_test.go calls PATCH /api/dustpan/v1/settings with access token and fails on main
- Implement hosted-service marker with fail-closed off and settings.locked refusal in NEW internal/settingsvc/smtp_settings.go Check: TestSmtpHostedPlainRefused calls PATCH /api/dustpan/v1/settings with access token and fails on main
- Implement SMTP connection check that reports reachability and writes an audit entry in NEW internal/settingsvc/smtpcheck.go Check: TestSmtpConnectionCheckAudit calls NEW POST /api/dustpan/v1/settings/smtp-check with access token and fails on main
- Add guide page and e2e step against a test server for SMTP settings read and check in NEW docs/guides/smtp-settings.md. Confirmed by: the driver's gates. Check: E2E smtp-settings-read calls GET /api/dustpan/v1/settings with access token against test server
- Run usability check from docs page alone for SMTP settings path. A person's step. Check: Usability run calls GET /api/dustpan/v1/settings and PATCH /api/dustpan/v1/settings with access token from docs page alone

Depends on:

- add-smtp-notification-sender-reads-transport-security
- `add-smtp-notification-sender-reads-message-safety` (the part before it in the split)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main declares sink settings plumbing (`pkg/dustpan/sinks.go:24 (func sinkSettings)`)
- Main hides the LDAP bind password in reads (`pkg/dustpan/ldap_test.go:163 (func TestLDAPProviderHidesTheBindPassword)`)
- Main defines settings locked error code (`internal/errcode/catalog_settings.go:9 (func settings.locked)`)
- Main defines settings patch-invalid error code (`internal/errcode/catalog_settings.go:12 (func settings.patch-invalid)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 7).

- Decisions that apply: 2 (extended), 7 (current).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5; ADR 373 extends decision 1. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365, 373.
- Quote: "SMTP settings are installation settings. The relay, the sender and the credentials of SMTP are installation settings with the precedence of ADR 63 decision 1."
- Quote: "The password is a sensitive setting (ADR 63 decision 6). It comes only from an environment variable or from a file that a `*_FILE` key names (ADR 63 decision 4)."
- Quote: "One settings API (owner decision 2026-10-18). The installation has one settings resource, and each namespace has one:"
- Effect: This part implements decision 2 password source and decision 7 settings visibility for the SMTP sink.
- Conflict: none.

**ADR 292: Plain LDAP and SMTP connections are refused unless an operator opts in** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 4 (current), 5 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The setting is visible (owner). The API shows its effective value and its source. Each change writes an audit entry."
- Quote: "The hosted service never turns it on (owner). The hosted service does not allow the setting. (owner)"
- Quote: "Fail closed. If the setting is missing or not valid, the value is off. A refusal names the address, the setting to change and the one command or call that changes it."
- Effect: This part implements decision 3 visibility and audit, decision 4 hosted-service refusal, and decision 5 fail-closed for the SMTP plain-connection opt-in.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Where does the SMTP password come from? ADR 212 decision 2 answers it: relay, sender and credentials are installation settings; password is sensitive and comes only from env or *_FILE, never flag or database, and PATCH of it answers 409.
- How are SMTP settings read and changed? ADR 212 decision 7 answers it: GET answers each declared setting with effective value and source; PATCH uses JSON Merge Patch, all-or-none, null deletes database value.
- How is the plain-connection opt-in shown, audited, and forced closed? ADR 292 decisions 3-5 answer it: visible effective value and source, audit entry per change, hosted service refuses the opt-in, missing or invalid means off with a refusal that names address, setting, and fix call.
- Does turning on plain SMTP need a fresh MFA step? ADR 212 decision 3 answers it: a change that reduces security needs a fresh MFA step; turning the plain opt-in from off to on reduces security and needs the step with error mfa.fresh-step-required.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

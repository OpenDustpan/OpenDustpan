# Review follow-ups (October): docs and guides

## 1. Document the confirmed app-registration case: PUT with If-Match returns 404
PR #1311

- Files: docs/reference/git-host-api.md:11; pkg/forge/registration.go:90; internal/api/server/githost_app.go:228; internal/api/server/githost.go:209
- Change: Document the confirmed app-registration case: PUT with If-Match returns 404 when the registration is absent, distinct from 412 for a version mismatch on an existing registration.
- Acceptance: Handler tests verify 404 for a missing registration, 412 for an existing registration with a mismatched version, and success with its current version. The reference explicitly distinguishes these cases.

## 2. Record the app-wide JWT budget across installations as a known gap in ADR 271,
PR #1311

- Files: docs/adr/0271-the-forge-port-git-host-connections-and-the-installation-level-github-app.md; pkg/forge/github/check.go:426
- Change: Record the app-wide JWT budget across installations as a known gap in ADR 271, or add an app-wide cap on the JWT calls.
- Acceptance: ADR 271 names the app-wide budget as a known gap, or a test shows the app-wide cap refuses calls beyond the limit across two installations.

## 3. Explain that layer 4 blocks literal pastes, not encoded or split credentials,
PR #1319

- Files: docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md:737; internal/stepup/stepup_test.go:37
- Change: Explain that layer 4 blocks literal pastes, not encoded or split credentials, and that challenge eligibility and credential binding prevent a pasted step-up credential alone from granting access. Add separate claim-propagation test contracts for job tokens and issued tokens, with explicit inputs and assertions.
- Acceptance: The RFC explicitly states the literal filter's limits and the protections supplied by layers 1 and 3. The test contracts require both token kinds to gain no step-up or fresh-authentication claim from a supplied step-up credential alone.

## 4. Define an authorized first-key bootstrap path for an SSO installation with no
PR #1319

- Files: docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md:488; docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md:498
- Change: Define an authorized first-key bootstrap path for an SSO installation with no step-up keys or WebAuthn factors. Reconcile that path with the administrator's existing key-level authentication requirement.
- Acceptance: The documentation gives a complete enrollment sequence from the no-factor state, including its authorization conditions and why unauthorized callers cannot use it.

## 5. Document that fallback text includes zero-width spaces for protection and that
PR #1344

- Files: docs/guides/slack-notifications.md:100-106
- Change: Document that fallback text includes zero-width spaces for protection and that copying it can preserve those characters.
- Acceptance: The guide explicitly states that a name such as app.prod copied from a fallback notification may contain invisible zero-width spaces.

## 6. Remove the unconditional legacy-delivery promise for chat destinations from the
PR #1350

- Files: docs/reference/notification-destinations-api.md:46; pkg/notify/route.go:313-338; pkg/notify/ports.go:454
- Change: Remove the unconditional legacy-delivery promise for chat destinations from the hostNeedsUpdate description. Distinguish webhook compatibility from the chat sender's ChatHost validation requirement.
- Acceptance: The API reference explicitly states that hostNeedsUpdate does not guarantee chat delivery and that chat URLs must pass send-time host validation. It must not promise delivery for a URL rejected by ChatHost.

## 7. Scope the documented legacy-delivery guarantee rather than promising that every
PR #1350

- Files: docs/reference/notification-destinations-api.md:46; pkg/notify/route.go:313-338
- Change: Scope the documented legacy-delivery guarantee rather than promising that every chat destination with hostNeedsUpdate still sends. Preserve the distinction between legacy webhook compatibility and strict chat host validation.
- Acceptance: The hostNeedsUpdate description distinguishes webhook and chat behavior. It makes no compatibility guarantee for chat URLs that ChatHost rejects.

## 8. Document that the fire route rejects Idempotency-Key values using the reserved
PR #1392

- Files: docs/reference/triggers-api.md:34
- Change: Document that the fire route rejects Idempotency-Key values using the reserved drift- prefix.
- Acceptance: The fire-route header section explicitly states that keys using the reserved drift- prefix are rejected.

## 9. Identify ADR 35 as the source of the superseded ports-and-services rule
PR #1479

- Files: docs/adr/0272-the-public-sdk-surface.md:97-99
- Change: Identify ADR 35 as the source of the superseded ports-and-services rule. Describe ADR 2 only as its historical origin if retaining that reference.
- Acceptance: The supersession table identifies ADR 35 as the current rule being replaced, and the surrounding text agrees. Any ADR 2 reference clearly describes historical provenance.

## 10. Update the launcher and source layout entries to reflect their move to
PR #1481

- Files: CLAUDE.md:102, CLAUDE.md:115
- Change: Update the launcher and source layout entries to reflect their move to internal/ rather than listing them under pkg/.
- Acceptance: The layout documents internal/launcher/ and internal/source/ and no longer lists those packages under pkg/.

## 11. Add the ongoing CI cost of supporting a third engine, including an additional
PR #1488

- Files: docs/rfc/0104-dialect-neutral-store-logic.md:761-809
- Change: Add the ongoing CI cost of supporting a third engine, including an additional real-server conformance job and its resource and maintenance requirements.
- Acceptance: The plan identifies the additional server-backed job, added runner and server resource use, and ongoing engine-version and CI maintenance.

---
_Generated by [Claude Code](https://claude.ai/code)_

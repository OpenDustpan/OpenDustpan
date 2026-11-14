---
version: 1
---

Issue: #1178.

## Problem

Site administrator connects the installation Slack app so teams receive chat messages as the app and act with acknowledge and approve buttons.

## Evidence

- No chat.postMessage caller exists on main outside tests. Proof: rg -n -F --glob '!*_test.go' 'chat.postMessage' pkg internal cmd matches 0
- chatops.Verifier exists on main with CheckTimestamp. Proof: internal/chatops/chatops.go:492 (func CheckTimestamp)
- Slack chat renderer package exists on main. Proof: pkg/notify/chat/slack/slack_test.go:217 (func TestRenderURLOnlyInButton)
- Acknowledge has no Slack inbound wiring on main. Proof: rg -n -F --glob '!*_test.go' 'acknowledge' pkg internal cmd matches 11

## Fix boundary

- Add sealed installation-scoped Slack app connection and NEW routes `POST /api/dustpan/v1/slack-connections`, `GET /api/dustpan/v1/slack-connections`, `GET /api/dustpan/v1/slack-connections/{id}`, `PUT /api/dustpan/v1/slack-connections/{id}/rotate`, `DELETE /api/dustpan/v1/slack-connections/{id}`, `POST /api/dustpan/v1/slack-links`, and `POST /api/dustpan/v1/slack-actions`; namespace sinks reference the connection by stable ID.
- Add connection-form chat sink that posts with chat.postMessage as the app.
- Add verified inbound actions acknowledge and approve; buttons render only on app sinks.

Out of scope:

- Microsoft Teams, Mattermost, and Matrix inbound paths.
- Slack webhook-form sink changes.
- Drift, jobs, and registry behavior.

## Check

- Add failing test for sealed bot token and signing secret in NEW pkg/notify/chat/slack/app_sink_test.go. Check: TestSlackConnectionSecretsSealed fails on main.
- Add failing test for chat.postMessage SendWithConnection in NEW pkg/notify/chat/slack/app_sink_test.go. Check: TestSlackPostAsAppFailsOnMain fails on main.
- Add failing test for HandleAction verify plus acknowledge and approve in NEW internal/chatops/slackaction_test.go. Check: TestSlackActionVerifyAndApplyFailsOnMain fails on main.
- Add failing test that webhook-form render holds no buttons in pkg/notify/chat/slack/slack_test.go:217. Check: TestSlackButtonsOnlyOnAppSink fails on main.
- Wire NEW connection routes POST, GET list, GET by ID, PUT rotate, DELETE with NEW actions slack-connection:manage and slack-connection:read on the installation object and audit entries, written in NEW internal/api/server/slack_connections.go. Check: `POST /api/dustpan/v1/slack-connections` returns sealed refs with access token.
- Wire NEW link route with NEW action slack-link:manage on the principal object and the HandleAction nonce check with 12 hour expiry, written in NEW internal/api/server/slack_links.go and NEW internal/chatops/slacknonce.go. Check: `POST /api/dustpan/v1/slack-links` maps Slack user to principal with console session.
- Wire NEW inbound actions route `POST /api/dustpan/v1/slack-actions` with NEW action slack-action:execute, verify, nonce, stored-grant checks and audit, written in NEW internal/api/server/slack_actions.go. Check: `POST /api/dustpan/v1/slack-actions` verifies and applies approve in NEW internal/api/server/slack_actions.go.
- Add e2e step with a fake Slack receiver in NEW e2e/slack_app_test.go, written against NEW routes declared in NEW internal/api/server/slack_connections.go and NEW internal/api/server/slack_actions.go; it calls `POST /api/dustpan/v1/slack-actions` with a signed Slack payload and `POST /api/dustpan/v1/slack-connections` with an access token. BLOCKED by those NEW routes until they land. Check: e2e/slack_app_test.go posts and applies approve against the fake receiver.

Depends on:

- `egress-allowlist-private-hosts-pkg` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Slack chat renderer package exists with button rule. (`pkg/notify/chat/slack/slack_test.go:217 (func TestRenderURLOnlyInButton)`)
- chatops.Verifier surface exists with CheckTimestamp. (`internal/chatops/chatops.go:492 (func CheckTimestamp)`)
- Incident acknowledge step exists, with no Slack wiring. (`internal/incident/incident_test.go:80 (func TestAcknowledgeSetsTheCommander)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- How does the Slack app sink relate to the existing webhook sink? RFC 98 is discussing and decides nothing. It keeps one sink kind chat with Platform slack. The Connection form references an installation-level Slack app connection and posts as the app. This change builds that form and no webhook change. No ADR text is in the pack, so no ADR decision applies.
- Can the existing chatops verifier verify inbound Slack actions? The pack shows internal/chatops/chatops.go:492 (func CheckTimestamp). This change reuses that timestamp check plus signature check and adds no new verifier type.
- Which ADR governs sealed sink secrets and audit records here? No ADR is in the pack. The pack lists RFC 98 as discussing. This change follows the RFC direction and touches no accepted ADR decision.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

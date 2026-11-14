## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for sealed bot token and signing secret in NEW pkg/notify/chat/slack/app_sink_test.go. Check: TestSlackConnectionSecretsSealed fails on main.
- [ ] 1.2 Add failing test for chat.postMessage SendWithConnection in NEW pkg/notify/chat/slack/app_sink_test.go. Check: TestSlackPostAsAppFailsOnMain fails on main.
- [ ] 1.3 Add failing test for HandleAction verify plus acknowledge and approve in NEW internal/chatops/slackaction_test.go. Check: TestSlackActionVerifyAndApplyFailsOnMain fails on main.
- [ ] 1.4 Add failing test that webhook-form render holds no buttons in pkg/notify/chat/slack/slack_test.go:217. Check: TestSlackButtonsOnlyOnAppSink fails on main.

## 2. Build and prove

- [ ] 2.1 Wire NEW connection routes POST, GET list, GET by ID, PUT rotate, DELETE with NEW actions slack-connection:manage and slack-connection:read on the installation object and audit entries, written in NEW internal/api/server/slack_connections.go. Check: `POST /api/dustpan/v1/slack-connections` returns sealed refs with access token.
- [ ] 2.2 Wire NEW link route with NEW action slack-link:manage on the principal object and the HandleAction nonce check with 12 hour expiry, written in NEW internal/api/server/slack_links.go and NEW internal/chatops/slacknonce.go. Check: `POST /api/dustpan/v1/slack-links` maps Slack user to principal with console session.
- [ ] 2.3 Wire NEW inbound actions route `POST /api/dustpan/v1/slack-actions` with NEW action slack-action:execute, verify, nonce, stored-grant checks and audit, written in NEW internal/api/server/slack_actions.go. Check: `POST /api/dustpan/v1/slack-actions` verifies and applies approve in NEW internal/api/server/slack_actions.go.
- [ ] 2.4 Add e2e step with a fake Slack receiver in NEW e2e/slack_app_test.go, written against NEW routes declared in NEW internal/api/server/slack_connections.go and NEW internal/api/server/slack_actions.go; it calls `POST /api/dustpan/v1/slack-actions` with a signed Slack payload and `POST /api/dustpan/v1/slack-connections` with an access token. BLOCKED by those NEW routes until they land. Check: e2e/slack_app_test.go posts and applies approve against the fake receiver.
- [ ] 2.5 Write docs page for connection, sink form, link flow, and actions in NEW docs/guides/chat-slack-app.md. Check: Docs page path completes from NEW docs/guides/chat-slack-app.md alone.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Meet definition of done for NEW routes declared in NEW internal/api/server/slack_connections.go and NEW internal/api/server/slack_actions.go with access token credential. Check: Routes declare authorization actions; fake-receiver e2e passes with access token; docs page exists; usability check passes; `make check` reports zero findings. BLOCKED by NEW routes `POST /api/dustpan/v1/slack-connections` and `POST /api/dustpan/v1/slack-actions`.

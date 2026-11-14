---
version: 1
---

Issue: #2707.

## Problem

As a Namespace administrator I connect the Git host and set notification sinks, so I need the guides to state the exact 404 versus 412 cases, the chat host check, and the fallback copy risk.

## Evidence

- Git-host API reference exists and can hold the 404 versus 412 note Proof: docs/reference/git-host-api.md exists
- Notification destinations reference exists and can hold the scoped chat delivery note Proof: docs/reference/notification-destinations-api.md exists
- Slack guide exists and can hold the zero-width space note Proof: docs/guides/slack-notifications.md exists
- Step-up RFC exists and can hold the literal-paste and bootstrap notes Proof: docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md exists

## Fix boundary

- Item 1: document PUT app-registration 404 absent versus 412 mismatch plus success in docs/reference/git-host-api.md verified against internal/api/server/githost_app.go and pkg/forge/registration.go
- Item 2: record app-wide JWT budget as known gap in docs/reference/git-host-api.md with no edit to ADR 271
- Item 3: clarify step-up layer 4 literal-paste limits with job-token and issued-token claim-propagation contracts in docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md and internal/stepup/stepup_test.go
- Item 4: write the first-key bootstrap enrollment sequence from the no-factor state with authorization conditions and reconciliation in docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md
- Item 5: note zero-width spaces in Slack fallback text in docs/guides/slack-notifications.md
- Item 6: remove the unconditional chat legacy-delivery promise from hostNeedsUpdate in docs/reference/notification-destinations-api.md
- Item 7: scope the legacy-delivery guarantee to webhook compatibility distinct from ChatHost send-time validation in docs/reference/notification-destinations-api.md

Out of scope:

- Behavior changes to handlers, Forge port, notify senders, or JWT limits
- Edits to any accepted ADR text
- Other October review items outside docs and guides

## Check

- Item 1: check main behavior for PUT app-registration absent, mismatch, and success Check: `rg -n "404|412|If-Match" docs/reference/git-host-api.md internal/api/server/githost_app.go pkg/forge/registration.go` shows the three cases
- Item 1: document PUT app-registration 404 versus 412 plus success in docs/reference/git-host-api.md Check: `grep -n "404" docs/reference/git-host-api.md` states absent gives 404, mismatch gives 412, current version succeeds
- Item 2: record app-wide JWT budget as known gap in git-host API reference Check: `grep -n "JWT budget" docs/reference/git-host-api.md` names the app-wide JWT budget gap, the refusal effect, and who sees it, with no edit to ADR 271
- Item 3a: clarify layer 4 literal-paste limits in RFC 102 Check: `grep -n "literal" docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md` states layer 4 blocks literal pastes only and layers 1 and 3 supply eligibility and binding
- Item 3b: add claim-propagation test contracts for job tokens and issued tokens in internal/stepup/stepup_test.go Check: `rg -n "job-token|issued token|claim" internal/stepup/stepup_test.go` shows both token contracts with inputs and assertions and no claim from a supplied credential alone
- Item 4: write the first-key bootstrap enrollment sequence in RFC 102 Check: `grep -n "bootstrap" docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md` gives the sequence from the no-factor state with authorization conditions, why unauthorized callers fail, and reconciliation with the key-level requirement
- Item 5: note zero-width spaces in Slack fallback text Check: `grep -n "zero-width" docs/guides/slack-notifications.md` warns that app.prod copied from fallback may hold invisible spaces
- Item 6: remove the unconditional chat legacy-delivery promise from hostNeedsUpdate Check: `grep -n "hostNeedsUpdate" docs/reference/notification-destinations-api.md` states no chat delivery guarantee for a ChatHost-rejected URL

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Chat host validation already exists at send time (`pkg/notify/route.go:328 (func ChatHost)`)
- Forge connection test suite already has registration cases (`pkg/forge/connectiontest/installation_cases.go:18 (func registrationCases)`)
- Test sink limits already cover refused addresses (`pkg/notify/sinks/limits_test.go:483 (func TestRefusedAddressCountsAgainstTheLimits)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does this change add an app-wide JWT cap or record the budget as a known gap? No. The issue allows either a gap note or a cap. This change records the gap note in the git-host API reference with no behavior change. A cap would be a behavior change outside this docs fix.
- Does recording the JWT budget gap edit ADR 271? No. An accepted ADR changes only by a later ADR. This change does not edit ADR 271. It records the app-wide JWT budget gap in the git-host API reference.
- Does the 404 versus 412 wording contradict any ADR? No. The reference text records existing version handling and changes no register or rotate rule.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Docs fixes

- [ ] 1.1 Item 1: check main behavior for PUT app-registration absent, mismatch, and success Check: `rg -n "404|412|If-Match" docs/reference/git-host-api.md internal/api/server/githost_app.go pkg/forge/registration.go` shows the three cases
- [ ] 1.2 Item 1: document PUT app-registration 404 versus 412 plus success in docs/reference/git-host-api.md Check: `grep -n "404" docs/reference/git-host-api.md` states absent gives 404, mismatch gives 412, current version succeeds
- [ ] 1.3 Item 2: record app-wide JWT budget as known gap in git-host API reference Check: `grep -n "JWT budget" docs/reference/git-host-api.md` names the app-wide JWT budget gap, the refusal effect, and who sees it, with no edit to ADR 271
- [ ] 1.4 Item 3a: clarify layer 4 literal-paste limits in RFC 102 Check: `grep -n "literal" docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md` states layer 4 blocks literal pastes only and layers 1 and 3 supply eligibility and binding
- [ ] 1.5 Item 3b: add claim-propagation test contracts for job tokens and issued tokens in internal/stepup/stepup_test.go Check: `rg -n "job-token|issued token|claim" internal/stepup/stepup_test.go` shows both token contracts with inputs and assertions and no claim from a supplied credential alone
- [ ] 1.6 Item 4: write the first-key bootstrap enrollment sequence in RFC 102 Check: `grep -n "bootstrap" docs/rfc/0102-cli-step-up-for-writes-that-need-a-fresh-authentication.md` gives the sequence from the no-factor state with authorization conditions, why unauthorized callers fail, and reconciliation with the key-level requirement
- [ ] 1.7 Item 5: note zero-width spaces in Slack fallback text Check: `grep -n "zero-width" docs/guides/slack-notifications.md` warns that app.prod copied from fallback may hold invisible spaces
- [ ] 1.8 Item 6: remove the unconditional chat legacy-delivery promise from hostNeedsUpdate Check: `grep -n "hostNeedsUpdate" docs/reference/notification-destinations-api.md` states no chat delivery guarantee for a ChatHost-rejected URL
- [ ] 1.9 Item 7: scope the legacy-delivery guarantee to webhook compatibility distinct from ChatHost Check: `grep -n "ChatHost" docs/reference/notification-destinations-api.md` distinguishes webhook compatibility from ChatHost send-time validation

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Docs lint passes with zero findings. Confirmed by: CI (docs-lint gate). Check: `make docs-lint` with zero findings. Confirmed by: CI (docs-lint gate).
- [ ] 3.2 Full check passes on the docs-only diff. Confirmed by: CI (full check gate). Check: `make check` with zero findings. Confirmed by: CI (full check gate).

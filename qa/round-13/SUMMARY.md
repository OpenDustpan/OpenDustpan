# QA round 13 summary

Scope: main from 76073e03 to e942f91b8. Local only. SQLite, Pulumi CLI 3.250.0, OpenTofu 1.12.7. PostgreSQL was not run (time).
Status: the GitHub issues were NOT filed. The permission classifier refused the issue-create step. The findings below are ready to file.

## Checked and fine
- Quickstart OpenTofu flow (init, apply, output).
- Two clients: three parallel `stack init` of one name gives one stack and two clear "already exists" errors. Two parallel `pulumi up` gives a 409 that names the other update and `pulumi cancel`.
- Restart of the server between updates: stacks and state stay.
- `dev --demo`: banner, rule in each demo namespace, demo sign-in with the printed password, stack init in `demo-platform`.
- Retention flags: bad values give an error that names the range and the flag.
- Placement conflict error names the hiding rule and the fix.

## Findings
1. Quickstart: the first `pulumi stack init acme/demo/dev` fails with 422 (no placement rule). The page has no step for a collection and a rule. `dev` does it, `serve` does not. (docs/guides/quickstart.md)
2. Passkey limits (docs/guides/passkeys.md, docs/reference/passkeys-api.md):
   - After 20 starts the first 429 has code `auth.rate-limited` and the text "too many failed sign-ins", though no sign-in failed. Later calls give `mfa.sign-in-source-limit`. The guide says the latter.
   - The 30-start budget is never reached before the 20-challenge cap.
   - No Retry-After header.
   - A sentence in the Limits section is out of order and repeated.
3. Small items: `namespace create` without a name prints only usage; `pulumi stack ls` shows console URLs on port 3000 that the quickstart never starts; passkey routes answer a generic 404 without `--public-url` and the text does not name it; an empty `--plan-retention` gives a parse error with no default hint; `allowCredentials` is absent, not empty as the reference says.
4. User docs still hold internal ADR/RFC references (about 40 lines in 20 files). This repeats the open issue 2410.

## Top 3 user-experience problems
1. A new user cannot make the first stack from the quickstart.
2. The passkey 429 gives a wrong reason and a different code from the guide.
3. Generic 404 and usage-only errors do not name the missing setting or flag.

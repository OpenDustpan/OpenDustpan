---
version: 1
---

Issue: #2493.

## Problem

As Operator, I keep the scan queue clean so real findings stay visible. False CodeQL alerts cost review time and hide real findings, so this change closes the 8 open alerts with written verdicts.

## Evidence

- Issue #2493 lists 8 CodeQL alerts as false positives with no code change and no PR. Proof: Issue #2493 body states All 8 alerts are false positives. No code change is needed, so there is no PR and its table lists alerts #13, #12, #11, #10, #9, #8, #3, #2 with verdict False positive.
- The flagged files exist on main. Proof: Pack paths list internal/api/server/signin.go, internal/authgate/authgate.go, internal/cli/settings_api.go, internal/cli/settings_test.go, internal/oauth/client.go, internal/oauth/method.go, internal/oauth/plain.go, pkg/accesstoken/accesstoken.go, pkg/blob/digest.go, pkg/blob/s3/store.go, pkg/identity/identity.go, pkg/setting/registry.go as exists true.
- Main has the symbols cited for alerts 13, 12, 11, 10, 8, 3, and 2. Proof: Pack main lists test TestSecretsHaveNoFlag at internal/cli/settings_test.go:324, func NewStaticTokens at pkg/identity/identity.go:1297, var ErrWeakToken at pkg/identity/identity.go:34, func newSecret at pkg/accesstoken/accesstoken.go:242, method request.recall at internal/authgate/authgate.go:451, func NewMD5 at pkg/blob/digest.go:34, method Client.Begin at internal/oauth/client.go:395, func oauth.CheckState at internal/oauth/client.go:383, method parseCallback at internal/api/server/signin.go:490, func session.NewSecret() at pkg/session/session.go:414, method TakeRequest at pkg/session/service.go:280, and func randomText at internal/oauth/client.go:567.
- Alert lines are use sites and pack lines are the checked constructors and helpers, so the numbers differ. Proof: Issue #2493 lists pkg/identity/identity.go:1203 and pkg/blob/digest.go:47 while pack main lists func NewStaticTokens at pkg/identity/identity.go:1297 and func NewMD5 at pkg/blob/digest.go:34.
- The pack does not prove the go.mod toolchain version or the Go 1.27 rand.Read claim. Proof: Pack code_facts hold rg counts for Secret, Sensitive, NewStaticTokens, minTokenLength, ErrWeakToken, AuthenticateToken, newSecret, remember, subtle.ConstantTimeCompare, and g.lookup, and no entry shows go.mod or a toolchain version.

## Fix boundary

- No code change. Dismiss CodeQL alerts 2, 3, 8, 9, 10, 11, 12, 13 as false positives for the reasons in the issue table.

Out of scope:

- Fixes to hashing, token storage, or OAuth state flow
- Changes to any flagged file

## Check

- Confirm each verdict reason against main and record the result on the issue. Check: rg over docs/adr/INDEX.md and docs/adr for hashing, token storage, and OAuth state shows no conflicting decision; go.mod toolchain noted; each flagged site re-read on main matches the issue table at 96ed862, else dismissal stops.
- Dismiss the 8 CodeQL alerts from the issue table with their false-positive reasons. Check: Each of alerts 2, 3, 8-13 shows dismissed as false positive with the reason from the issue table.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a test that secrets have no flag. (`internal/cli/settings_test.go:324 (TestSecretsHaveNoFlag)`)
- Main has the static-token constructor that refuses short tokens. (`pkg/identity/identity.go:1297 (func NewStaticTokens)`)
- Main has the weak-token error used by the length check. (`pkg/identity/identity.go:34 (ErrWeakToken)`)
- Main has the token secret generator in the access-token package. (`pkg/accesstoken/accesstoken.go:242 (func newSecret)`)
- Main has the per-request recall helper cited for the auth gate cache. (`internal/authgate/authgate.go:451 (func request.recall)`)
- Main has the MD5 integrity helper for blobs. (`pkg/blob/digest.go:34 (func NewMD5)`)
- Main has the OAuth client Begin and state check. (`internal/oauth/client.go:395 (func Client.Begin)`)
- Main has the OAuth state check helper. (`internal/oauth/client.go:383 (func oauth.CheckState)`)
- Main has the sign-in callback parser. (`internal/api/server/signin.go:490 (func Handler.parseCallback)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Can a no-code false-positive dismissal close without a failing test?
   - Options: Dismiss the 8 alerts with no failing test and record reasons in the issue table; Add a regression test that guards each dismissed site.
   - Provisional decision (owner review 14 Nov): Dismiss the 8 alerts with no failing test and record reasons in the issue table
   - Reason: No behavior changes, so no spec scenario can fail on main. The confirm task re-reads each site before dismissal, which is the check that fits this change. Benchmark: Google suppresses a false-positive finding with a written reason and keeps no code test for it.
   - Persona impact: No persona sees a behavior change; the dismissal record is the issue table plus the CodeQL entries. Escape hatch: If CodeQL re-raises an alert, re-open the issue and run the confirm step again before any new dismissal.

Questions considered and answered:

- Does any alert need a code fix? No code fix. Issue #2493 records all 8 alerts as false positives against main at 96ed862 and states no code change is needed, so there is no PR. The change only confirms each site on main and dismisses the alerts.
- Does an ADR govern hashing or OAuth state here? The pack lists no ADRs and no RFCs. The change edits no file and changes no decision text. The confirm task checks docs/adr/INDEX.md and runs rg over docs/adr for hashing, token storage, and OAuth state before dismissal, so a conflicting decision stops the dismissal.
- What does the pack prove for each verdict reason? Only what the pack proves: the flagged files exist, the constructors and helpers named in main exist, and the rg counts show which strings exist. The safety reasons come from the table in issue #2493 and each site is re-read on main by the confirm task before dismissal.
- Is the Go 1.27 crypto/rand.Read claim proved? It is not proved by the pack. The pack gives no go.mod toolchain version. The verdicts for alerts 2 and 3 do not rely on it here; the confirm task reads go.mod and client.go randomText before dismissal, and a failed check stops the dismissal.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

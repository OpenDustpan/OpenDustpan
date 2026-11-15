---
version: 1
---

Issue: #3203.

Short form: bug (found by the security review of #3055) and a test gap.

Waits for: #3055

## Problem

**Persona: platform engineer (Priya).** Priya reads `expiresInDays` of a token
and the expiry warning of the CLI. For a token with a far expiry, the count is
negative.

`DaysUntil` in `pkg/accesstoken/accesstoken.go` rounds a duration up with
`int64((d + day - 1) / day)`. `time.Time.Sub` saturates at about 292 years.
For a far expiry, `d + day - 1` wraps to a negative value. Input: now
2026-11-15, expiry `9999-12-31T23:59:59Z`. Answer: `-106750`.

The effect is a wrong count in the warning text and in `expiresInDays`. Access
does not change: the token status, the warnings and the authentication path
compare durations, not this count.

The `d <= 0` guard of `DaysUntil` has no test that fails without it. The past
case of `TestDaysUntil` is 1 ms in the past, which answers 0 with or without
the guard (the issue reports a mutation run: the guard removed, 195 tests
passed).

## Evidence

- `DaysUntil` is not on `origin/main`: `rg -n DaysUntil pkg/accesstoken` finds
  nothing. It lands with PR #3055 (change `token-days-left-ceiling`):
  `git grep -n DaysUntil origin/pr3055 -- pkg/accesstoken` finds
  `pkg/accesstoken/accesstoken.go:182` (comment) and `:186` (function), and the
  test `TestDaysUntil` with six rows, the last one `past_expiry_answers_0`
  at `now.Add(-time.Millisecond)`. So the change waits for #3055.
- The wrong line is `return int64((d + day - 1) / day)` on that branch.

## Fix boundary

Work in `pkg/accesstoken/accesstoken.go` and `pkg/accesstoken/accesstoken_test.go`
only, after #3055 is on `main`.

- Round up without the addition: `n := d / day; if d%day != 0 { n++ }`, then
  `return int64(n)`. The rule stays "started days, at least 1 while the expiry
  is ahead, 0 once it passed".
- Add to `TestDaysUntil` a row for a far expiry (`9999-12-31T23:59:59Z`, with
  `now` as in the test) that expects a count above 100000. `time.Duration`
  saturates at `math.MaxInt64` ns, so the count is `ceil(MaxInt64 / day)`,
  106752. The row checks "positive and above 100000", not the exact value.
- Add a row for an expiry 2 days in the past that expects 0. A row 1 ms in the
  past stays.
- No change to the token status, the warning thresholds or authentication.

## Check

- `go test ./pkg/accesstoken/ -run TestDaysUntil -v` passes.
- Mutation 1: restore `int64((d + day - 1) / day)`. The far-expiry row fails
  with a negative count.
- Mutation 2: remove the `d <= 0` branch. The row 2 days in the past fails
  (the count is `-2`, or the rounded value of a negative duration, not 0).
  The row 1 ms in the past still passes, which is the gap.
- `make check` reports nothing.

## ADR check

- `rg -n -i "expiresInDays|DaysUntil|days left" docs/adr` finds ADR 53 and
  ADR 69 only.
- ADR 53 decision 11: "Expiry warnings in milestone 4. Before an access token
  expires, the server warns in four ways: a field in the API response for the
  token, a banner in the console, an email to the owner of the token [...], a
  warning in the CLI." The change fixes the count of the field and the
  warning. It adds no way and no threshold. ADR 53 records owner answers
  (2026-10-11 to 2026-10-13: "API field + console banner, Email, CLI
  warning"); the change keeps all of them. Changed-by chain: ADR 69 (extends
  decisions 2 and 11: the default maximum lifetime of 365 days), ADR 70, ADR
  212. None changes the rounding or the count.
- ADR 250 (defense in depth) asks for a test that fails when a guard is
  removed. Task 1.3 gives it for the `d <= 0` guard. Not owner-tagged.
- No owner-tagged decision conflicts.

## Open questions

none. Considered: (1) exact value of the far count: the row checks a lower
bound, because the saturated value depends on `time.Duration`, not on a rule
of the product. (2) a clamp at 292 years: not needed, the count stays correct
up to the saturation and a larger expiry is not a real input. (3) the branch
of #3055 changes before it merges: the tasks name the function and the test,
not line numbers; the driver rebases after #3055 lands.

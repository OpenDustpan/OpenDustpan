Issue: #3030.

## Problem

A person or a script reads `expiresInDays` of an access token right after it is
made. A 90-day token answers 90 in the millisecond of its creation and 89 from
the next millisecond. The value rounds down, so it is one less than the
lifetime that the person just chose. In the last day of an active token it
answers 0, which reads as "expired" while the token still works. The e2e test
`TestAccessTokens/sqlite` reads the token list twice, a few milliseconds
apart, and compares the two bodies byte for byte. It failed 2 of 12 runs on the
head of #3014 (the issue gives the runs).

## Evidence

- `pkg/accesstoken/accesstoken.go:184-190`: `DaysLeft` returns
  `int64(t.ExpiresAt.Sub(now) / day), true`, where `day = 24 * time.Hour`
  (`:147`). Integer division of a `time.Duration` rounds down.
- `pkg/accesstoken/service.go:143-145`: `Service.Now` truncates the clock to the
  millisecond. A token made at `T` has `ExpiresAt = T + 90 days`. At `T` the
  quotient is exactly 90; at `T + 1 ms` it is 89.
- `internal/api/server/tokens.go:201-204`: `tokenOf` sets `ExpiresInDays` from
  `DaysLeft(now)`, with one `now` for each answer.
- `internal/api/server/tokens.go:87-89`: the field comment says "the count of
  whole days until the expiry, at the time of the answer by the server clock".
  The value depends on the time of the answer by contract, so it cannot be
  computed from the stored times alone.
- `e2e/tokens_test.go:128-157`: `checkTokenNeedsFreshAuth` reads
  `GET /api/server/tokens` before and after three refused calls and requires
  `after != before` to be false.
- `pkg/accesstoken/accesstoken_test.go:1138-1158`: `TestCreatedByAndDaysLeft`
  checks only the value at the creation instant, which is the one instant where
  floor and ceiling agree. No test reads the value 1 ms later.
- `rg -n "DaysLeft" --type go` gives the definition, the test above and
  `internal/api/server/tokens.go:202`. No other caller.
- `docs/guides/access-tokens.md:82`: "`expiresInDays` | The whole days left,
  for an active token."

## Design

`DaysLeft` rounds up: it answers the number of started days left. For an active
token, `d = ExpiresAt - now` is greater than 0 (`StatusAt` reports expired at
`ExpiresAt`), so the result is `int64((d + day - 1) / day)`, at least 1.

- A 90-day token answers 90 for the whole of its first day, 1 in its last day,
  and null once it is expired. The value never shows 0 for a token that works.
- The value changes only at `ExpiresAt - k*day`. The first change of a new token
  is 24 hours after its creation. A test run that reads a list twice within one
  run, minutes after it made its tokens, cannot see a change. The race of the
  issue becomes impossible, not less likely. The e2e comparison stays byte for
  byte.
- The unit test uses the fixture's fixed clock and steps it, so the test does
  not depend on wall-clock time.
- `ExpiryWarning` uses durations, not this count, and does not change.

Delight check: the number that a person reads right after the creation is the
number that the person chose. A script that warns on `expiresInDays <= 7` warns
one day later at most, and still before the expiry.

## Fix boundary

**IN**

- `pkg/accesstoken/accesstoken.go`: `DaysLeft` rounds up, and its comment says
  "started days left, at least 1 for an active token".
- `pkg/accesstoken/accesstoken_test.go`: a table test of `DaysLeft` with the
  fixed clock (section "Check").
- `internal/api/server/tokens.go:87-89`: the field comment says "rounded up".
- `docs/guides/access-tokens.md:82`: "The days left, rounded up, for an active
  token. A token in its last day answers 1."
- The OpenAPI description text of `expiresInDays`, if it has one, through its
  generator, and the clients and golden files through theirs.

**OUT**

- No change to the e2e test: it keeps `expiresInDays` in the comparison.
- No change to `ExpiryWarning`, the thresholds, the stored times, the schema,
  the status rules or the audit.
- No new clock type: the `Service` clock and the `now` argument already give one
  reading for each answer.

## Check

- Guard 1, the first day answers the chosen lifetime. Test:
  `TestDaysLeft/creation_plus_1ms_answers_90` and
  `TestDaysLeft/creation_plus_one_day_minus_1ms_answers_90`. Mutation: restore
  the floor; both rows fail with 89.
- Guard 2, the value steps once a day. Test:
  `TestDaysLeft/creation_plus_one_day_answers_89`. Mutation: add one more day to
  the rounding; the row fails with 90.
- Guard 3, an active token never answers 0. Test:
  `TestDaysLeft/expiry_minus_1ms_answers_1`. Mutation: restore the floor; the
  row fails with 0.
- Guard 4, an expired token has no count. Test:
  `TestDaysLeft/at_expiry_is_not_active`. Mutation: return `true` for every
  status; the row fails.
- The e2e test `TestAccessTokens` passes 12 of 12 runs on the head with
  `-count=12` for the sqlite case.

## ADR check

- **ADR 53 decision 11.** "Before an access token expires, the server warns in
  four ways: - a field in the API response for the token, ..." Changed by:
  ADR 69 and ADR 212 extend decision 11 (the default maximum lifetime of 365
  days; the SMTP settings of the expiry email). Current rule: this ADR, with
  ADR 69, 70, 212, 237, 354 (`docs/adr/INDEX.md`, row 53). The ADR gives no
  rounding rule for the count. `rg -n -i "days left|expiresInDays|whole day"`
  over ADR 69, 212, 237 and 354 finds no line. The change keeps the field and
  the warning. No conflict.
- **The API rules of CLAUDE.md.** The tool-neutral API may change before 1.0,
  and the field keeps its name and type. No conflict.

No conflict with an owner-tagged decision.

## Open questions

none. Questions considered:

- Floor or ceiling? Ceiling, for the reasons in "Design": the value right after
  creation equals the chosen lifetime, an active token never shows 0, and the
  first change is 24 hours away, which removes the race.
- Must the e2e test drop the field? No. The issue forbids it, and the change
  makes the comparison stable.
- Compute the count from the stored `createdAt` and `expiresAt`? No. The field
  is "at the time of the answer" by contract, so it needs a clock reading. One
  reading per answer exists already.
- Does a script that relies on the old floor break? There are no users and no
  tag yet. The guide says the new rule.

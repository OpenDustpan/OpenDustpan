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
  `internal/api/server/tokens.go:202`. No other caller of `DaysLeft`.
- `internal/cli/client/expiry.go:58`: the CLI warning computes its own count,
  `int(expiresAt.Sub(e.now()) / hoursPerDay)`, and prints "in %d days". It also
  rounds down, so the CLI says "in 0 days" for a token that still works. It is a
  second copy of the rule.
- `tokenOf` builds the token in the create, rotate, read and list answers
  (`internal/api/server/tokens.go:501`, `:640`, `:745`, `:799`), so one change
  of `DaysLeft` covers all four.
- `pkg/accesstoken/service.go:233-240`: a zero expiry takes the default
  lifetime, so every token has an expiry, and every active token has
  `expiresInDays`.
- `internal/api/apidesc/openapi.json` describes `/api/dustpan/v1` only
  (`internal/api/server/openapi.go:11`). It has no `expiresInDays` field, so no
  generated file changes.
- `docs/guides/access-tokens.md:82`: "`expiresInDays` | The whole days left,
  for an active token."

## Design

One function holds the rule: `accesstoken.DaysUntil(expiresAt, now time.Time)
int64`, exported from `pkg/accesstoken` because the CLI client already imports
that package for `accesstoken.ExpiryHeader`. It answers the number of started
days left: for `d = expiresAt - now > 0`, `int64((d + day - 1) / day)`, at
least 1; for `d <= 0`, 0. `DaysLeft` calls it for an active token, and the CLI
warning calls it instead of its own division. The server and the CLI then
cannot disagree about the count (ADR 364: one place for the rule, an exported
name by choice, with its doc comment).

For an active token, `d` is greater than 0 (`StatusAt` reports expired at
`ExpiresAt`, `pkg/accesstoken/accesstoken.go:139`).

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
- A revoked, disabled or expired token answers `null`, as today
  (`DaysLeft` returns false for every status but active, `:185`).
- Two reads in the last day both answer 1 until the expiry. Two reads that fall
  on each side of a step (`ExpiresAt - k*day`) still differ, by design: the
  field is "at the time of the answer". No test reads a token's list across 24
  hours.

Delight check: the number that a person reads right after the creation is the
number that the person chose. A script that warns on `expiresInDays <= 7` warns
one day later at most, and still before the expiry.

## Fix boundary

**IN**

- `pkg/accesstoken/accesstoken.go`: add `DaysUntil` with its doc comment;
  `DaysLeft` calls it, and its comment says "started days left, at least 1 for
  an active token".
- `internal/cli/client/expiry.go`: the warning calls `accesstoken.DaysUntil`;
  remove `hoursPerDay` if nothing else uses it. Its test asserts the count of a
  token with 23 hours left (1) and 24 hours and 1 ms left (2).
- `pkg/accesstoken/accesstoken_test.go`: a table test of `DaysLeft` with the
  fixed clock (section "Check").
- `internal/api/server/tokens.go:87-89`: the field comment says "rounded up".
- `docs/guides/access-tokens.md:82`: "The days left, rounded up, for an active
  token. A token in its last day answers 1."

**OUT**

- No change to the e2e test: it keeps `expiresInDays` in the comparison.
- No change to `ExpiryWarning`, the thresholds, the stored times, the schema,
  the status rules or the audit.
- No new clock type: the `Service` clock and the `now` argument already give one
  reading for each answer.
- No rule for a server clock that steps back. The count follows the server
  clock, as every time in the API does; a clock step is an operator fault
  outside this change.
- No generated file: `openapi.json` does not describe this route.

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
- Guard 4b, a partial day rounds up. Test:
  `TestDaysUntil/one_day_and_1ms_answers_2` and
  `TestDaysUntil/23h59m59s_answers_1`. Mutation: restore the floor; the rows
  fail with 1 and 0.
- Guard 5, the CLI says the same count. Test: the expiry-warning test of
  `internal/cli/client` with 23 hours left prints "in 1 days". Mutation:
  restore the division in `expiry.go`; the test fails with 0.
- Guard 4, a token that is not active has no count. Rows
  `TestDaysLeft/revoked_is_not_active` and `TestDaysLeft/disabled_is_not_active`
  also. Test:
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
- Does the CLI warning change too? Yes, it calls the same function (adversarial
  pass by another provider, 13 Nov).
- Which OpenAPI text? None: `openapi.json` does not describe this route.
- What if the server clock steps back? Out of scope; see "Fix boundary".
- Revoked, disabled, partial-day, last-day and boundary cases: answered in
  "Design" and "Check" (same pass).

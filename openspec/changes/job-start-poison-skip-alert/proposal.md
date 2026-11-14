---
version: 1
---

## Why

**Persona: operator.** An operator runs a server and needs to know when job starts fail again and again. A poison job start is skipped so the queue keeps moving, and the skip leaves no signal today. The operator needs a count and an alert at 10 skips.

The user path this change must make work:

1. **Server start.** The operator starts the server. The skip counter starts at 0 and is registered with the metrics.
2. **API.** The operator reads the counter at `/api/server/metrics`.
3. **API.** At the 10th skip the server sends one alert to the destination that the operator configured.
4. **Docs page.** The operator reads how to find the skipped starts and how to set the alert.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The operator has set no alert destination, so the alert at 10 skips goes nowhere. | The server writes a log record and keeps the counter. The record says that no alert destination is set. | `curl -H "Authorization: Bearer $TOKEN" https://HOST/api/server/metrics` |

Evidence for the claims above:

- The metrics route exists on main. Proof: The route ANY /api/server/metrics is in the route table of the pack.
- The pack names no ADR, RFC or issue text for the skip counter. Proof: The pack lists adrs, rfcs and issues as empty arrays. The metric name and the channel are therefore provisional.
- A metric name check exists on main. Proof: internal/metrics/metrics.go:620 (func validMetricName)

## What Changes

- Add a counter of skipped poison job starts (NEW type).
- Send one operator alert when the count reaches 10 skips (NEW type).
- Show the counter in the metrics output.

Out of scope:

- Other items of milestone 5, such as the job routes and the runner work.
- Change to how a start is judged poison.

Depends on:

- job-start-follow-ups-1321
- `job-start-follow-ups-1321` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The operator has to set an alert destination first. The log record names it when it is missing.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The metric name check exists. (`internal/metrics/metrics.go:620 (func validMetricName)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which alert channel carries the operator alert at 10 skips?
   - Options: The notification sinks of the installation; A log record only; A new dedicated channel.
   - Provisional decision (owner review 14 Nov): The notification sinks of the installation
   - Reason: The operator already configures sinks. No new surface is added. If no sink is set, the log record and the counter remain. Benchmark: AWS CloudWatch alarms send to a configured notification topic, not to a fixed channel.
   - Persona impact: The operator keeps one place for alerts. No persona is upset. Escape hatch: The counter stays in the metrics output for any scraper.

Questions considered and answered:

- Does an ADR decide the metric name or the alert channel? The pack shows no ADR for them. Both are provisional decisions and go to the open question.
- Does the change touch an owner-tagged decision? The pack lists no ADR, so no owner-tagged decision is touched.
- Does the alert carry security semantics? No. It carries a count and a unit reference only, and it grants no access. The PM decides.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

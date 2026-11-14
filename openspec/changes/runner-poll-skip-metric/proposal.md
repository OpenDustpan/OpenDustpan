---
version: 2
---

## Why

**Persona: runner operator.** A runner operator runs a runner pool and wants to know why a queued job waits. When a poll reaches the bound of 64 candidate jobs, it skips the jobs after the bound. Today nothing shows this skip. The operator cannot tell a full queue from a pool with no matching runner. A counter on the metrics route shows the skip.

The user path this change must make work:

1. **Server start.** The operator starts the server. The counter is registered with a value of 0.
2. **API.** The operator reads the metrics route and finds the skip counter.
3. **Docs page.** The operator reads the metrics page, which names the counter and its meaning.
4. **E2e.** A test fills a pool queue past the bound, polls, and reads the counter from the metrics route.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| The caller has no right to read the metrics route. | The server answers 403 with the error code of the route. | curl -H "Authorization: Bearer $TOKEN" https://HOST/api/server/metrics |

Evidence for the claims above:

- The poll function that applies the bound is in the runner protocol file. Proof: Pack paths: internal/api/runnerapi/protocol.go exists; roadmap step names func pollOnce.
- The server has a metrics route and a metric-name check. Proof: Pack main: route ANY /api/server/metrics and func validMetricName at internal/metrics/metrics.go:620.
- No ADR in the pack decides the metric name. Proof: Pack roadmap step: a metric name that no ADR decides is a provisional decision; the adrs list is empty.

## What Changes

- Count each job that pollOnce skips at the bound of 64.
- Expose the count as one counter on the metrics route.

Out of scope:

- Change of the bound of 64.
- Change of the order in which a poll picks jobs.
- Wake of a long poll.

Depends on:

- runners
- `job-start-follow-ups-1321` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The operator can tell a full poll bound from an empty pool with one read of the metrics route.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The metrics package has a metric-name check. (`internal/metrics/metrics.go:620 (func validMetricName)`)
- The runner protocol file holds the poll function pollOnce. The pack gives no line number. (`internal/api/runnerapi/protocol.go:1 (func pollOnce)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does a label with the pool name make the metric unbounded? The design uses no label, so the number of series stays 1.
- Does the metric carry a secret? No. It is one number with no job or runner data.
- Who decides the metric name? No ADR decides it and it is not security semantics, so the PM decides it by the project rule for provisional decisions.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

## Context

Nightly CI workflow, a budgets file and a compare program. No server runtime code changes and no new tool. Service containers give PostgreSQL, an S3-compatible store and Toxiproxy.

## Decisions

1. **One workflow with service containers, timeout and concurrency.** The NEW workflow `.github/workflows/nightly-perf.yml` has `on.schedule` and `on.workflow_dispatch` only, concurrency group `nightly-perf` with cancel-in-progress false, `timeout-minutes` 120, and three jobs: `load`, `soak`, `toxiproxy-faults`. Each job runs the NEW wrappers `perf/run-load.sh`, `perf/run-soak.sh` and `perf/run-toxiproxy.sh`, writes the NEW artifact `measurements.json`, and uploads it with `if always()`. The NEW program in `perf/compare` checks `perf/budgets.json`. Third-party actions pin to a full commit SHA. Permissions are `contents: read`, plus `issues: write` on the compare job only.
2. **Budgets file, artifact schema and comparison rule.** `perf/budgets.json` is keyed by measurement name, for example NEW measurement name load.checkpoint.save.p95. Each entry holds p50, p95, max, unit and scope. `measurements.json` holds a list of objects {name,p50,p95,max,unit,scope}. A breach is a measured p50, p95 or max above the budget field of the same name, with zero tolerance. A measurement with no budget entry is record-only. A budget entry for a measurement that is no longer produced gives a warning and never fails the job. Each measurement is evaluated alone. Budget changes need a reviewed pull request.
3. **Issue deduplication, recovery and infrastructure failures.** One regression keeps one issue with the exact title `[nightly-perf] <measurement>`, matched exactly. A new breach opens the issue with name, measured value, budget, unit, scope and run id. A repeat breach adds a comment. A recovery closes the issue with a comment. If a `gh issue` call fails, the step fails the job, keeps the artifact and makes no open or close. A run with no measurement (container failure, timeout, cancellation) fails with no regression issue. The workflow never runs on `pull_request`, so fork pull requests do not run it and get no secrets.

## Structure

No server structure changes. The only stored reference is `perf/budgets.json`, keyed by stable measurement names, never by branch or runner names.

## Risks

- Budget drift when first values are accepted without review; a human approves the first budgets pull request.
- Noisy runners cause false breaches; the recovery comment and the reviewed budget update handle it, with no automatic budget change.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| job duration | complete within 120 minutes | minutes | nightly-perf workflow per run |
| soak duration | run real CLI processes for 30 minutes | minutes | soak job per run |

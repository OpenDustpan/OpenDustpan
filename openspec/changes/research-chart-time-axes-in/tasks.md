## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Side effects (computed from main)

- [ ] 1.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 2. Research

- [ ] 2.1 Collect precedent for Material, Fluent, Grafana, and Arabic and Hebrew financial sites and record it in NEW docs/research/chart-time-axes-rtl.md with links first, at least 5 sources, and no stored third-party screenshot unless its license allows it. Check: NEW docs/research/chart-time-axes-rtl.md exists with precedent table for Material, Fluent, Grafana, one Arabic and one Hebrew financial site, each with source link, access date, product version, axis direction, digit form, and mixed-content behavior.
- [ ] 2.2 Record user-expectation evidence in NEW docs/research/chart-time-axes-rtl.md separate from product precedent. Check: NEW docs/research/chart-time-axes-rtl.md cites a study, survey, or native-speaker review for whether users expect time to run right to left or left to right.
- [ ] 2.3 Write the recommendation, disagreement rule, mixed-content rule, and amendment statement in NEW docs/research/chart-time-axes-rtl.md. Check: NEW docs/research/chart-time-axes-rtl.md states one recommendation with the disagreement rule, mixed-content rule, and which ADR holds the future rule plus the follow-up change name; `make docs-lint` passes for the new file.

## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Docs links

- [ ] 1.1 Edit docs/adr/0026-opentofu-state.md to add the Changed-by ADR 281 line with verb Changes for decision 2, with no edit to decision text. Confirmed by: the driver's gates. Check: rg -n 'ADR 281 changes decision 2' docs/adr/0026-opentofu-state.md
- [ ] 1.2 Edit docs/adr/0037-checkpoints-in-blobs-and-delta-uploads.md to add the Changed-by ADR 281 line with verb Changes for decision 2, with no edit to decision text. Confirmed by: the driver's gates. Check: rg -n 'ADR 281 changes decision 2' docs/adr/0037-checkpoints-in-blobs-and-delta-uploads.md
- [ ] 1.3 Edit docs/adr/0043-pulumi-journaling-support.md to add the Changed-by ADR 281 line with verb Changes and no decision number, with no edit to decision text. Confirmed by: the driver's gates. Check: rg -n 'ADR 281 changes' docs/adr/0043-pulumi-journaling-support.md
- [ ] 1.4 Edit docs/adr/0060-audit-log-port-and-log-sinks.md to add the Changed-by ADR 281 line with verb Changes for decision 18, with no edit to decision text. Confirmed by: the driver's gates. Check: rg -n 'ADR 281 changes decision 18' docs/adr/0060-audit-log-port-and-log-sinks.md
- [ ] 1.5 Edit docs/adr/0281-any-s3-compatible-store-the-database-decides-which-write-wins.md to add Changes lines for decision 11 to ADRs 26, 37, 43 and decision 12 to ADR 60. Confirmed by: the driver's gates. Check: rg -n 'Changes:' docs/adr/0281-any-s3-compatible-store-the-database-decides-which-write-wins.md

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Run make adr-index in docs/adr/INDEX.md with no hand edit of the index. Confirmed by: the driver's gates. Check: make adr-index leaves docs/adr/INDEX.md with ADR 281 in the Current rule column of ADRs 26, 37, 43 and 60
- [ ] 3.2 Run make docs-lint for the link self-test and form check. Confirmed by: the driver's gates. Check: make docs-lint passes with zero findings

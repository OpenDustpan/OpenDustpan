# SQL store: instructions for AI coding agents

These rules add to the rules in the root `AGENTS.md`. They do not replace them.

## Queries

- Write SQL by hand for each dialect. Put fixed queries in
  `pkg/store/<dialect>/queries.sql` and generate them with sqlc
  (`make generate`). Never edit `internal/db`.
- Put a new query block in its place in the name order of `queries.sql`,
  never at the end of the file: next to the queries of its area, in the name
  order of those queries. Do not sort the existing blocks.
- Write dynamic queries by hand in the adapter. Use placeholders only.

## Schema

- Until the first tag, each dialect has one migration file, the baseline in
  `pkg/store/<dialect>/migrations/` (ADR 322).
- To change the schema, edit the baseline of each dialect in place. Put a new
  object in its place in the name order of the baseline, not at the end, so
  that parallel changes edit different lines.
- Do not add a numbered migration. Do not write migration, upgrade, alias or
  data-conversion code for a shape that no tag shipped.
- The store records the SHA-256 of each migration and refuses a database of
  another version of the baseline with `ErrSchemaDiverged`. An edit of a
  comment changes the SHA-256 too. For dev, run `opendustpan dev reset`
  (ADR 324).
- Timestamps are Unix milliseconds in `BIGINT`.

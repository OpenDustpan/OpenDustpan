## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Benchmark runs

- [ ] 1.1 Survey how two outside agent products hold a run that waits for approval. Check: Survey table names two outside products and maps each to suspend, poll, or end and restart.
- [ ] 1.2 Fix the prompt set, server build, model and setup for the three hold patterns. Check: Lab record lists build, database, model, provider, cache mode, 3 prompts, 5 repeats, median and range.
- [ ] 1.3 Measure model token cost per hold pattern. Check: Table shows input, output, cache read and cache write counts from the token ledger over 5 repeats.
- [ ] 1.4 Measure memory, poll load and time to resume. Check: Table shows peak memory in megabytes for 1 and 50 waiting runs and seconds from approval to next model call.
- [ ] 1.5 Observe gaps, restarts, takeover, cancel, limits, expiry and revocation. Check: Matrix shows resume, repeat, loss and one-change result for each row.

## 2. Definition of done

- [ ] 2.1 Add the new file docs/explanation/dusty-wait-benchmark.md with method, numbers and recommendation. Check: The file exists and holds method, survey, tables, matrix and one recommended pattern.
- [ ] 2.2 Check that the note repeats from the docs page alone. Check: A second reader repeats the setup from the note and reproduces each table.
- [ ] 2.3 Run the docs lint. Check: `make docs-lint` reports zero findings.

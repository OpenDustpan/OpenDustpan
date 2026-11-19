# Worked example: OpenDustpan/OpenDustpan

This file shows the report that the skill gives for one real repository. It is a record of
19 November 2026. The numbers go stale: run the commands again before you use them for a
decision.

How the steps went:

- **Step 1.** The session clone had 50 commits. `git fetch --deepen=3000 origin main` gave
  2,310. With the shallow clone, no pattern below was visible.
- **Step 2.** `scripts/churn.py --ref origin/main`, then the GitHub records: the last 100
  runs of `ci.yml` and the list of open pull requests.
- **Step 3.** About 40 signals became 8 causes.
- **Step 4.** `docs/adr/INDEX.md` and `rg` over `docs/adr` and `docs/rfc`. The project decided or repaired three causes. That changed three proposals.

---

# Toil report: OpenDustpan/OpenDustpan

Read: 2,310 commits on `origin/main`, 9 October to 19 November 2026 (41 active days,
median 41 commits a day, 180 on the busiest day). Two commit authors: the project owner
(1,519) and coding agents (785). CI: the last 100 completed runs of `ci.yml`. Pull
requests: the 62 that were open.
Not read: review comments, CI logs, the `OpenDustpan/console` repository.

## Summary

The repository takes work from many agents in parallel. The toil is where parallel changes
meet: a small set of files that each feature must edit, and plans that name details of a
main branch that moves 41 times a day. The project already repaired two such points
(numbered migrations, the committed TypeScript client). The same repair applies to four
more. Do finding 1 first: it removes the two hottest hand-written files and one rule
from the agent instructions.

## Findings, most expensive first

### 1. Each store change edits the same two SQL files

- **Pattern:** the hand-made twin, and the shared list.
- **Evidence:** `pkg/store/postgres/queries.sql` (223 commits on 32 of 41 days, 6,565
  lines, 842 queries) and `pkg/store/sqlite/queries.sql` (210 commits, 6,017 lines) are the
  two hottest hand-written files. They change together in 141 commits (share 0.67). Each
  has one generated file that follows it (share 0.70). On 16 October the SQLite file got
  20 commits in one day.
- **Cost:** about 1 commit in 10 opens both files and regenerates both outputs. Two open
  pull requests that add a query can conflict in four files.
- **Cause:** sqlc reads one query file for each dialect, so all domains share it. The agent
  instructions compensate with a rule: "Put a new query block in its place in the name
  order of `queries.sql`, never at the end of the file".
- **Status in the project:** decided, with a cost. Two hand-written dialects are a decision
  and stay. The one-file layout is not a decision.
- **Repair (rank 3, change the shape):** point `queries:` in `sqlc.yaml` at a directory
  with one file for each domain area (`queries/audit.sql`, `queries/job.sql`). sqlc then
  writes one Go file for each. Use the same file names in both dialects so that the twin
  of a file is evident.
- **First step:** move the queries of one area (for example `audit`) in both dialects, run
  `make generate`, and check that `sqlc diff` is clean.
- **Shows that it worked:** no file under `pkg/store/*/queries/` is in the first 10 hot
  files, and the name-order rule leaves `AGENTS.md`.

### 2. Each feature edits a set of central lists

- **Pattern:** the shared list.
- **Evidence:** commits that touch each file: `pkg/dustpan/dustpan.go` 209 (4,026 lines),
  `e2e/coverage.go` 142, `pkg/store/postgres/backup.go` 143,
  `pkg/store/sqlite/backup_columns.go` 103, `internal/api/server/routes_test.go` 102
  (2,131 lines), `pkg/backup/portable_tables.go` 89, `.golangci.yml` 88. The `domain` rule
  of depguard in `.golangci.yml` lists 72 package paths, one for each domain package.
- **Cost:** a feature with a table and a route edits 5 to 7 files that all other features
  also edit. The cost of a conflict here is one more CI run (median 13 minutes) and one
  more update from `main`.
- **Cause:** wiring, backup columns, route authorization tests and lint scope are each
  one central file.
- **Status in the project:** not decided as a whole. ADR 364 points the same way: prefer a
  structure over an added check.
- **Repair (rank 2 and 3):**
  - Backup column lists: a test already compares them with the live schema. A list that a
    test can check against a source can be derived from that source. Generate the column
    lists from the baseline migration, and keep by hand only the choice that a person
    makes (which tables are portable).
  - depguard: the comments in `.golangci.yml` say that a broad glob matched wrong paths.
    A deny-by-default glob for `**/pkg/**` with a short list of exceptions (`pkg/store`,
    `pkg/dustpan`, the adapters) could replace 72 entries. Check this with the adapters
    before you propose it as a change.
  - `dustpan.go` and `routes_test.go`: let each domain give its routes, options and
    authorization cases from its own package, and let the central file loop over them.
- **First step:** the backup column lists. They are the most mechanical.
- **Shows that it worked:** commits in a week that touch `backup_columns.go`,
  `portable_tables.go` and `.golangci.yml` go below 5 each.

### 3. Open plans name details of a main branch that moves

- **Pattern:** the plan that goes stale.
- **Evidence:** 205 commits (9%) have a subject that starts with `openspec` or `spec`. At
  least 51 of them only bring a plan in line with the code: "rewrite ... against main"
  (#3636), "open changes name the moved caller routes" (#3357), "hourly spec fixes 4" to
  "11", "passes the reground check". `openspec/changes` holds 236 open changes and 41
  archived ones. 6 of the 62 open pull requests rewrite a change "against main".
- **Cost:** about 1 commit in 45 is a correction of a plan, each with a full pull request
  and a CI run. The count is a lower bound: it uses only the subject words.
- **Cause:** tasks in a change name file paths, route paths and error codes. `main` moves
  41 times a day, so an open change goes stale in days. 236 open changes is more than the
  agents can start before the changes go stale.
- **Status in the project:** partly repaired. The "reground" check finds a stale change at
  the time of use. The inventory is not limited.
- **Repair (rank 1, remove the need):** keep a proposal (the intent and the decisions)
  for work that is not scheduled, and write `tasks.md` with its paths when an agent starts
  the change. The reground check then runs one time, on a new plan.
- **First step:** count the open changes that no pull request implements, and stop the
  periodic fix runs for those.
- **Shows that it worked:** the share of `openspec:` commits that add or archive a change
  goes up, and the share that corrects one goes down.

### 4. More open work than the pipeline merges without a meeting

- **Pattern:** more open work than the pipeline can merge.
- **Evidence:** 62 open pull requests, 57 of them opened in a window of 3.5 hours on
  19 November. 100 CI runs completed in 2 hours 44 minutes: 17 failed and CI canceled 16
  (33% gave no merge). A successful run takes 13 minutes (median) and 21.6 (p90). 39
  commits are merge trains that fold many pull requests into one ("Train 2: #1529 #1530
  ...").
- **Cost:** one third of the CI minutes. This report did not read the logs, so the split
  between real defects and stale branches is not known.
- **Cause:** findings 1, 2 and 3. The number of pairs of branches that can conflict grows
  with the square of the open branches that touch the same files.
- **Status in the project:** partly repaired: the merge queue, and path filters that skip
  the Go jobs for changes of documents only (#3211, #3213).
- **Repair:** do findings 1 and 2 first. Then limit the open pull requests for each hot
  area, not for the repository.
- **First step:** for one day, record for each failed run if the cause was the change or
  the base.
- **Shows that it worked:** the share of failed and canceled runs goes below 15%.

### 5. Decision records share counters and index files

- **Pattern:** the conflict magnet.
- **Evidence:** `GLOSSARY.md` 138 commits, `docs/rfc/README.md` 110,
  `docs/adr/INDEX.md` 82 (30 on 1 November). `.repowise/decisions.yaml` has one line of
  41,411 bytes, 39 commits, and changes together with `INDEX.md` (share 0.85). ADR numbers
  collide: "renumber ADR 312 to 314 as ADR 313 to 315".
- **Cost:** each new ADR edits two generated index files and can take a number that a
  parallel pull request also took.
- **Cause:** a sequence number and two committed generated files that each ADR must edit.
- **Status in the project:** ADR 383 decided the same problem for the TypeScript client:
  generated files must have no line that each change edits. It does not name these files.
- **Repair (rank 2):** apply ADR 383 here. Write `decisions.yaml` with one short line for
  each field, or generate it where Repowise reads it. Give an ADR its number when it
  merges, and use a slug before that time.
- **First step:** change the writer of `decisions.yaml` to many lines.
- **Shows that it worked:** `churn.py` prints no conflict magnet.

### 6. The agent instructions grow by 7 lines a day

- **Pattern:** the growing rule file.
- **Evidence:** `AGENTS.md` grew from 155 to 436 lines in 76 commits. `Makefile`: 36 to
  453 lines and 67 targets. `.github/workflows/ci.yml`: 47 to 846 lines.
- **Cost:** each agent session reads all 436 lines. Each rule is also a possible review
  finding.
- **Cause:** an incident becomes a rule. Some rules compensate for a structure (the query
  order rule of finding 1, "resolve a conflict in a generated file with a new run").
- **Status in the project:** ADR 364 prefers structure and lint configuration to new
  checks. No decision limits the size of the file.
- **Repair (rank 1 and 3):** for each rule that starts with "put", "never" or "do not",
  name the structure that removes it, and remove the rule with the repair. Move the rules
  of one area (store, security code, voice) to files that load only for that area.
  `.claude/rules/` exists.
- **First step:** remove the rules that findings 1 and 5 make unnecessary.
- **Shows that it worked:** the line count of `AGENTS.md` goes down for two weeks in
  sequence.

### 7. Six lint rules have more than 100 suppressions each

- **Pattern:** the suppressed check.
- **Evidence:** `//nolint:gosec` 290, `errcheck` 275, `gochecknoglobals` 168,
  `tagliatelle` 165, `exhaustruct` 127, `ireturn` 120, `paralleltest` 108. A CI job
  (`nolint-check`, #2725) checks new suppressions.
- **Cost:** about 1,250 lines, each with a reason that a reviewer must read.
- **Cause (could be):** `tagliatelle` checks JSON names, and the tool-compatible APIs must
  keep the names that the tools expect. `ireturn` flags constructors that return a port.
  This report did not read the suppressions one by one.
- **Status in the project:** not decided.
- **Repair (rank 1):** exclude the wire packages from `tagliatelle` in `.golangci.yml`,
  and give `ireturn` an allowlist of the port interfaces. Sample 20 suppressions of
  `gosec` and `errcheck` before you propose something for them.
- **First step:** `tagliatelle`.
- **Shows that it worked:** the count of each marker in `churn.py`.

### 8. Tests that depend on the load of the machine

- **Pattern:** the check that depends on the machine.
- **Evidence:** 42 subjects (2%) name a race, a timeout, a slow store or a deadlock: "Make
  the store conformance cases hold on a slow store" (#1486), "Fix two load-sensitive test
  flakes" (#1517), "order the deadlock timers" (#3309), and an open pull request "Make the
  postgres swap deadlock test independent of machine load" (#3595).
- **Cost:** not measured. A run that fails for this cause costs 13 to 22 minutes.
- **Cause:** tests of locks and leases use real time. Test code has only 2 `time.Sleep`
  calls, so the cause is deadlines and budgets, not sleeps.
- **Status in the project:** repaired case by case. A test clock exists (#3602).
- **Repair (rank 3):** make the test clock the only way to get a deadline in the store
  conformance suites.
- **First step:** list the tests that failed and then passed with no code change in the
  last week of CI runs.
- **Shows that it worked:** that list gets shorter.

## Resolved (seen in the history, repaired since)

- Numbered migrations: the history added and later removed 400 migration files, with subjects
  like "number the lock record migration 0163 again". ADR 322 replaced them with one
  baseline for each dialect.
- The committed TypeScript client: six files that followed `openapi.json` with a share of
  0.64 to 0.72, and one description on one line. ADR 383 and the change "generate the
  TypeScript client on demand" took it out of Git.
- CI hosting changed four times in October (own machines, Blacksmith, own machines "again,
  for now", GitHub-hosted on 6 November). No flip since.

## Not toil

- `e2e/e2e_test.go` and `e2e/harness.go`: 49 and 36 commits on 11 October. That was the
  first build of the suite.
- 2.2 million changed lines in 6 weeks, with 4% generated and 17% prose: growth of a new
  product.
- One revert in 2,310 commits.

## Order of work

1. Finding 1 (split the query files): the most conflicts removed for one mechanical
   change.
2. Finding 5 (`decisions.yaml` on many lines): one small change to a script.
3. Finding 2, the backup column lists first.
4. Finding 3 (write tasks at the start of the work): needs a decision of the owner, so
   write it as an RFC.
5. Findings 6 and 7 follow from 1, 2 and 5.
6. Measure finding 4 and finding 8 again after the others. Their numbers should fall
   without a direct repair.

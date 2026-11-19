# Patterns of toil

Each pattern gives the signal, the usual cause and the repairs, with the repair that
removes the cause first. The section names of `scripts/churn.py` are in _italics_.

## Contents

1. The shared list
2. The hand-made twin
3. The committed copy
4. The conflict magnet
5. The growing rule file
6. The plan that goes stale
7. More open work than the pipeline can merge
8. The check that depends on the machine
9. The suppressed check
10. The workshop under construction
11. The step that only a person can do
12. Write, then delete

## 1. The shared list

**Signal.** A file of wiring or of names is in _Hot files_, and each commit adds or changes
a few lines in it. Examples: a route table, a dependency-injection file, a list of tables,
a list of packages in a lint rule, a coverage list, an index document, a changelog.

**Cause.** Each new thing must be registered in a central place. All parallel changes meet
in that file. The file grows without limit and becomes hard to review.

**Repairs.**

- Derive the list: find the things by a path pattern, a naming rule, a type or a build
  step. A lint rule can name `pkg/**` in place of forty package paths.
- Move each entry next to the thing that it registers (one file for each entry, or a
  registration in the package of the thing), and collect the entries in one loop.
- If the list must stay, keep it sorted and one entry on each line, so that two additions
  merge without a conflict.

## 2. The hand-made twin

**Signal.** Two hand-written files are in _Files that change together_ with a high share:
two SQL dialects, a server type and a client type, a schema and a validator, code and the
document that repeats it, a test fake and the real thing.

**Cause.** One decision is written in two places, and a person keeps them equal.

**Repairs.**

- Generate one from the other, or both from one source.
- If the project chose two hand-written forms, make the pair cheap: put the twins in files
  with the same name and the same order, split large files by area so that one change opens
  two small files, and let one conformance test fail with a message that names the missing
  twin.

## 3. The committed copy

**Signal.** Generated files, golden files, snapshots or lock files are in _Hot files_, and
a large share of the changed lines in _Where the changed lines go_ is generated. Commit
subjects say "regenerate". CI has a "generated files are current" check that fails.

**Cause.** The output of a generator is in Git. Each change of the source needs a new run,
and two changes to the source conflict in the output.

**Repairs.**

- Do not commit the output. Generate it in the build, or publish it as a package.
- If the output must be in Git (for review, or for a consumer that reads the repository),
  give it a layout that merges: many small files, one item on each line, no digest and no
  time in the file.
- A bot that generates again after a conflict is a repair of rank 4. It needs CI capacity
  and it can starve when the main branch moves fast.

## 4. The conflict magnet

**Signal.** A file in _Conflict magnets_ (one very long line), or a hot file to which
each change adds at the end, or a file with a counter (a version number, a migration
number, "next free ID").

**Cause.** Git merges by line. Two changes to one line always conflict, and two additions
at the same position conflict too. A counter makes each pair of parallel changes conflict
by design.

**Repairs.**

- Remove the shared line: write one item on each line, and remove digests and counters
  from committed files. Give the counter at merge time or at release time.
- Use names or content hashes in place of sequence numbers.
- Insert in sorted order, not at the end.

## 5. The growing rule file

**Signal.** `CONTRIBUTING.md`, `AGENTS.md`, `CLAUDE.md` or a checklist is in
_Configuration, tooling and rule files_ with many commits and a line count that grows
fast. The rules say "remember to", "never", "after X, also do Y", "put it in order, not at
the end".

**Cause.** Each incident became a sentence that each person and each agent must read and
obey. The rule compensates for a structure that permits the error. A long rule file also
costs attention on each task, and agents obey a long file less reliably than a short one.

**Repairs.**

- For each rule, ask which structure makes the rule unnecessary. "Put a new query in name
  order" goes away when each area has its own file.
- Move a rule that a tool can check into the tool that the project has.
- Move rules for one area into a file that is loaded only for that area.
- Give the file a size budget. A new rule must replace an old one or become a mechanism.

## 6. The plan that goes stale

**Signal.** Many commits on specs, tickets-in-files, task lists or design documents with
subjects like "align with main", "rewrite against main", "name the moved file", "tick
task". Many open plan documents and few archived ones.

**Cause.** Plans are written long before the work and name details that change: file
paths, route names, line numbers, error codes. Each merge to the main branch can make each
open plan wrong.

**Repairs.**

- Write the detailed plan just before the work starts. Keep only a short intent before
  that time.
- Name behavior and stable identifiers in a plan. Let the implementer find paths.
- Limit the number of open plans. A plan that nobody will start this week is inventory.
- Check plans against the main branch by machine at the time of use, not by rewriting
  them on a schedule.

## 7. More open work than the pipeline can merge

**Signal.** Many open pull requests of the same age. Many CI runs for each hour, with a
large share canceled or replaced. Commit subjects with "train", "batch", "fold", "merge of
main". Pull requests that need more than one update from the main branch before they merge.

**Cause.** Work starts faster than it can merge. Each merge makes the other open branches
stale. The cost grows with the square of the number of open branches that touch the same
files.

**Repairs.**

- Repair the shared lists and the conflict magnets first (patterns 1 to 4). They decide
  how many branches can be open without a conflict.
- Limit the open work for each area of the code, not for the whole repository.
- Make CI cheap for the change that it gets: run only the jobs that the changed paths
  need.
- Merge small changes in a queue that tests them together.

## 8. The check that depends on the machine

**Signal.** Commit subjects with "flaky", "slow", "timeout", "raise the budget", "hold
under load", "deadlock". CI runs that pass on a second attempt. The same test name in many
fix commits.

**Cause.** A test measures real time, uses a shared resource or depends on the order of
events that the machine decides.

**Repairs.**

- Give tests a clock that the test controls, and make the real clock hard to reach in
  test code.
- Wait for an event, not for a time.
- Give each test its own resource (database, port, directory).
- Record the tests that fail and then pass, and treat the list as a queue of defects. A
  retry without a record hides the cost.

## 9. The suppressed check

**Signal.** One marker in _Suppression markers in the tree_ occurs many times (more than
about 50 for one rule), or the list of excluded paths of a scanner grows.

**Cause.** The configuration of the check does not fit the code. Each suppression costs a
line, a reason and a review question.

**Repairs.**

- Configure the rule: exclude the paths or the names where it cannot apply (for example
  wire formats of another project), or give it an allowlist.
- Repair the code pattern once, with a helper that needs no suppression.
- If the rule finds nothing that the team repairs, switch it off and say why.

## 10. The workshop under construction

**Signal.** The CI workflow, the Makefile, the lint configuration or the container file is
in _Hot files_. Subjects flip a choice: "use X", "use Y", "use X again, for now".

**Cause.** Decisions about the tooling are made by experiment on the main branch. Some of
this is a normal cost of a new project.

**Repairs.**

- Find the choices that flipped more than one time (the runner host, the cache, the job
  layout) and write down what was learned, so that the next person does not try again.
- Keep the CI file thin: it calls the same commands that a person runs on their machine.
- Measure before the next change: the duration of each job, the cache hit rate.

## 11. The step that only a person can do

**Signal.** The same manual instruction occurs in many pull request bodies, review
comments or rule files: "run the generator", "update the pin", "paste the output", "ask the
owner". One author has most of the commits of a mechanical kind.

**Cause.** A step needs a permission, a secret, a decision or a machine that only one
person has.

**Repairs.**

- If the step is mechanical, give it to CI or to a bot.
- If the step is a decision, make the question small and collect the questions in one
  place, so that the person answers ten in one sitting.

## 12. Write, then delete

**Signal.** _Short-lived files_ has many entries in one directory. Subjects say "remove
again", "move back", "rename". The lines deleted in a week come near the lines added.

**Cause.** A design changed after the code was written. Some of this is learning and is
worth its cost. It is toil when the same kind of thing is written and removed more than
one time (numbered migrations that are later squashed, a directory that moves twice).

**Repairs.**

- Find the decision that changed, and ask what a cheaper experiment could have shown
  first: a prototype, a short design note, one instance before many.
- If a later decision already stopped the pattern, list it as resolved.

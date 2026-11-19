---
name: fix-my-life
description: >
  Find the toil in a repository and propose how to remove it. Use when the user asks why
  work in a repository is slow, repetitive or painful, asks what to fix in the process or
  the tooling, or reports constant merge conflicts, rework, flaky checks, CI trouble or
  configuration files that change all the time. Also use for "where does our time go",
  "fix my life", a toil or churn audit, and a developer-experience review. This skill
  reads the Git history, the configuration and the CI records, and writes a ranked report
  with evidence. It does not change the repository.
---

# Fix my life: find toil and churn, propose the repair

**Toil** is work that repeats, that a machine or a better structure could remove, and that
does not make the product better. **Churn** is change to the same lines or files again and
again. Churn in new product code is growth. Churn in configuration, generated files, lists,
rule files and plans is usually toil that left a mark in the history.

The history shows what people did. It does not show what they planned. So the history is
the best witness of toil: each workaround, each second try and each conflict is a commit.

The result of this skill is a report. Do not edit code, configuration or documents of the
repository unless the user asks for a repair after they read the report.

## Steps

### 1. Get the full history

A shallow clone hides most of the evidence. Run
`git rev-parse --is-shallow-repository`. If the answer is `true`, deepen the clone
(`git fetch --unshallow`, or `git fetch --deepen=5000 origin <default branch>`). Read the
default branch (for example `origin/main`), not the work branch of the session.

Done when: you know the number of commits and the date range that you can read, and you
state both in the report.

### 2. Collect the signals

Run the bundled script. It only reads.

```bash
python3 -I <skill directory>/scripts/churn.py --ref origin/main
```

Add `--since "3 months ago"` when the history is long: toil that stopped long ago is not a
finding. The script prints these signals: hot files, files that change together, rework in
one day, words in commit subjects, churn and growth of configuration and rule files, the
share of generated lines, files with one very long line, short-lived files and suppression
markers.

Then collect what the history cannot show. Use the tools that the session has:

- CI records: the share of failed and canceled runs, the duration, the number of runs for
  each hour, runs that passed on a second attempt.
- Open pull requests: the number, the age, how many touch the same hot files.
- The rule files for people and agents (`CONTRIBUTING.md`, `AGENTS.md`, `CLAUDE.md`): each
  rule that says "remember to", "never" or "after you change X, also change Y" marks a
  place where the structure does not do the work.
- The decision records of the repository (ADRs, RFCs, design documents, issues), if it
  has them.

Done when: you have each script section and you looked for each of the four items
above. If a source is not available, say so in the report.

### 3. Turn signals into findings

A number is not a finding. For each strong signal, read 5 to 10 of the commits behind it
(`git log --format='%ad %s' --date=short -- <file>`, then `git show --stat <sha>`) and
answer three questions:

1. **Why did this change again?** New product behavior is growth. Leave it. A second try,
   a list that each feature must edit, a copy that must follow its source, a conflict, a
   check that failed for a reason outside the change: these are toil.
2. **What is the one cause?** Many signals have one cause. Four hot files can be one
   decision that people write in four places. Group the signals by cause and not by file.
3. **How much does it cost?** Count how often it occurs (commits in a week, share of pull
   requests) and what one occurrence costs (minutes of a person, one CI run, one conflict,
   one review round).

Read `references/patterns.md` now. It names the usual patterns, the signal of each one and
the repairs that remove the cause. Use it to name what you see. Do not force a finding into
a pattern: a new pattern is a good result.

Done when: each strong signal is in a finding, or you can say why it is growth.

### 4. Check what the project decided

Before you propose a repair, search the decision records and the recent history for the
subject. Three results are possible:

- **Decided and done.** The toil is in the history, but a later change removed the cause.
  Put it in the report as resolved, in one line, so that the reader knows that you saw it.
- **Decided, with a cost.** The project chose this cost for a reason (for example two SQL
  dialects written by hand). Do not propose to reverse the decision. Propose how to make
  the chosen path cost less, and say that the decision stays.
- **Not decided.** Propose a repair.

This step is the difference between a report that people use and a report that tells them
what they know.

### 5. Propose repairs

For each finding, prefer the first repair in this order that is possible:

1. **Remove the need.** Remove the copy, the list or the step.
2. **Derive it.** Make the copy from its source: at build time, or at run time, and not
   committed. A committed copy needs a check, and the check is new toil.
3. **Change the shape so that changes do not meet.** One file for each entry in place of
   one shared list. A registration next to the thing that it registers.
4. **Automate the step.** A bot or a script does the work that a person does now.
5. **Add a check or a rule.** This is the last choice. A check moves the toil to the moment
   of failure. A rule moves it to the memory of each person and each agent.

Each repair needs a first step that fits in one small change, and a number that shows
that it worked (for example "commits that touch `X` in a week go from 30 to less than 5").

### 6. Write the report

Use this structure. Write to the place that the user names. If they name none, answer in
the conversation.

```markdown
# Toil report: <repository>

Read: <N> commits on <branch>, <first date> to <last date>. CI: <what you read>.
Not read: <sources that were not available>.

## Summary
<3 to 5 lines: the largest costs, and the one repair to do first.>

## Findings, most expensive first

### 1. <The cause, in plain words>
- **Pattern:** <name from references/patterns.md, or a new name>
- **Evidence:** <numbers, files, 2 or 3 commit subjects or shas, the command that shows it>
- **Cost:** <how often> x <cost of one occurrence>
- **Cause:** <why the structure makes people do this>
- **Status in the project:** <not decided | decided, with a cost (reference) | partly repaired (reference)>
- **Repair:** <the repair, and its rank in the order of step 5>
- **First step:** <one small change>
- **Shows that it worked:** <a number that the script or CI can give again>

## Resolved (seen in the history, repaired since)
<one line each, with the change that repaired it>

## Not toil
<strong signals that are growth, one line each, so the reader does not ask>

## Order of work
<the repairs in the order to do them, with the reason. A repair that removes a conflict
point comes before a repair that needs many parallel changes.>
```

Rules for the report:

- Give the number and the command for each claim. A reader must be able to run it again.
- Keep the hedges honest. Subject words and co-change are evidence, not proof. Write
  "could" where you did not read enough commits to be sure.
- Rank by cost, not by how interesting the cause is. Five findings with numbers are better
  than fifteen without.
- Blame the structure, never a person. The author of most commits is the person who paid
  the cost.

## Worked example

`references/example-opendustpan.md` applies these steps to OpenDustpan/OpenDustpan:
2,310 commits in 41 days, most of them by coding agents that work in parallel. Read it
when you want to see the size of a finding, how a signal becomes a cause, and how the
decision records of a project change what you propose.

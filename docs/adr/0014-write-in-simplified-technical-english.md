# 14. Write in Simplified Technical English

Status: accepted · 2026-10-10

## Context

Agents write most of the prose in this repository: documentation, ADRs, error
messages and instructions for other agents. Agents and people read it without
a chance to ask the writer what a sentence means. Ambiguous English costs more
here than in most projects, because a misread error message or agent
instruction leads to wrong code.

ASD-STE100 Simplified Technical English is a controlled language from the
aerospace industry. It removes words with more than one meaning and sentences
with more than one possible structure. We compared three agent skills that
apply it:

- `danyuchn/asd-ste100-skill`: written for text that agents and machines parse.
  It keeps hedges as content and ships a linter that we can run in CI.
- `AminBlg/SimpleEnglish`: more mature and measured with evals, but it bans
  "may", "might" and "could", and it also changes how chat replies look.
- `menonpg/ste-prompt`: two days old on 2026-10-10, one author, no linter and
  no tests.

## Decision

- Use `danyuchn/asd-ste100-skill` for all English prose in this repository.
  Strict mode applies to error messages, log messages, API error text, CLI
  output and instructions for agents that we write. STE-flavored mode applies to
  documentation, ADRs, the glossary, pull requests, commits and code comments.
- Keep hedges. A sentence that says "may" must not become a sentence that
  states a fact. This is the main reason we chose this skill.
- Install agent skills with `npx skills` for Claude Code and OpenCode. The
  files live in `.agents/skills/<name>`, and `.claude/skills/<name>` links to
  them. `skills-lock.json` records the GitHub source and the content hash of
  each skill. Other skills, such as `karpathy-guidelines`, use the same install.
- Pin the skill to upstream commit `3419838` (2026-09-10), because ADR 7 allows
  only versions at least two weeks old. The CLI cannot fetch a commit, so the
  first install used a local checkout of that commit. The lock entry names the
  GitHub source, so `npx skills update` works for later updates.
- Turn off the telemetry of the CLI with `DISABLE_TELEMETRY=1`.
- `make docs-lint` runs the linter of the skill on the README, `CLAUDE.md`,
  `GLOSSARY.md` and ADRs from 14 on, as part of `make check`. Hard findings,
  such as semicolons and sentences over 25 words, fail the check. Passive
  voice and compound tenses are advisory.
- ADRs accepted before ADR 14 are exempt, because ADR 1 forbids edits to them.
- Skills from other projects under `.agents/skills/` are exempt. We do not edit
  them, and `make docs-lint` does not check them.

## Consequences

- `make check` needs Python 3. The linter uses only the standard library.
- CI must run `make docs-lint` too. The workflow file needs that step before
  the gate applies to pull requests.
- The linter checks Markdown files only. Error messages and log messages in Go
  code follow the rules through review, not through a gate.
- The console repository has the same need for UI text. It needs its own
  decision.
- Before an update, check that the upstream commit is at least two weeks old,
  and read its diff. A skill runs with the full permissions of the agent.

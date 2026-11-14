## Context

Owner decision, 13 Nov 2026 (queue P22): the written rule stands for skills until the owner gives the words. The claim in commit 65e6990c is unverified. The change edits CLAUDE.md; AGENTS.md is the same file.

## Decisions

1. **Keep the written rule and mark the claim unverified.** Add one sentence to the skill line of CLAUDE.md. No ADR is written.
2. **Record the known breach.** The gh-stack skill is installed at content newer than two weeks. This stays a known breach until the owner gives the words or says to pin it. This change does not change the installed skill.

## Structure

No code structure change. ADR 7 stays the one source of the rule.

## Risks

- The gh-stack skill stays at content newer than two weeks until the owner decides.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Docs text only; no runtime behavior changes. |  |  |

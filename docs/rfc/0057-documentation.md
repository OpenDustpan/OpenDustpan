---
rfc: 57
title: Documentation, examples and agent skills as a product
status: draft
created: 2026-10-10
---

# RFC 57. Documentation, examples and agent skills as a product

Status: draft. A stub that records the goal and the research it needs. The
research is not done yet.

## Goal

OpenDustpan must have the best user-facing documentation, examples and AI
agent skills in its space, better than the commercial products. Even where
the product is weaker, the docs must not be.

## Scope

- User docs for operators (install, upgrade, back up, run) and for users of
  each tool (Pulumi CLI, OpenTofu).
- The API reference (ADR 17: every feature has a REST API).
- Examples that run, and that a test keeps working.
- Skills and machine-readable docs for AI agents, alongside the MCP server and
  the self-describing API (RFC 41).
- Translation (RFC 11) and the voice rules (ADR 14, ADR 16).

## Research needed

Deliberate research, academic where possible, before any decision:

- Which tools and products, in and outside IaC, have users who love their
  docs? What evidence shows it (surveys, studies, public feedback), and why do
  their users love them?
- What does research on developer documentation say about what makes docs,
  API references and examples work, and about how docs fail?
- Which structures and processes hold up over time: content models, docs as
  code, tested examples, linting, versioning, feedback loops, metrics such as
  time to first success?
- What makes docs and skills work for AI agents, and how is that measured?

The findings go in one Markdown file with a source for each claim, and this
RFC then proposes the approach.

## Open questions

- Where do the docs live: this repository, the console repository, or a docs
  repository of their own?
- Which docs tool, given the rules on dependencies and the STE docs gate?
- How are examples tested in CI, for both tools?
- Who owns the docs, and does a feature count as done without them (the
  definition of done in `AGENTS.md`)?

## Outcome

Accepted in part as [ADR 204](../adr/0204-a-user-facing-feature-ships-with-its-docs-page.md)
on 2026-10-18: a user-facing feature is done only with its docs page in the
same pull request. The other questions stay open.

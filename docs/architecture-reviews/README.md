# Architecture reviews

This folder keeps the reports of architecture reviews of the server. An
agent writes a report with the `/improve-codebase-architecture` skill, as a
nightly run or as a one-off review. Each report has a Markdown file and a
self-contained HTML file with the same content. The name of each file is the
date of the review.

## How to read a report

- A candidate is a claim to verify. It is not a decision.
- A bug in a report comes from reading the code. Write a failing test for it
  before you change the code.
- File and line references point to the commit that the report names. Later
  commits can move them.
- Decisions go in issues, RFCs and ADRs (ADR 15). When a candidate gets a
  verdict, record the verdict there, and link the report.

## Rules for a new report

- Do not edit a report after you add it. Write a new report instead.
- A report can cite ADRs and RFCs, because it is a research note.

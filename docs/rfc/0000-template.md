---
rfc: 0
title: A short title that names the question
status: draft
created: YYYY-MM-DD
---

# RFC 0. A short title that names the question

Status: draft.

## Problem

What is wrong or missing, and who it affects. Give the facts and the numbers.

## Options

Each option with what it costs and what it gives. Include "do nothing".

## Proposal

The option the author prefers, and why. Leave it out when there is no
preference yet.

## Open questions

What has to be answered before a decision.

## Checklist

- [ ] Tolerates asynchronous replication between installations: each namespace writes only its own half, under its own fence, and no write waits for another installation (ADR 248 decisions 3, 6 and 9).

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).

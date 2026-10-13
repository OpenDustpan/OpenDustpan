---
rfc: 60
title: Wait until every instance sees maintenance mode before a restore applies
status: accepted
created: 2026-10-11
adr: 48
---

# RFC 60. Wait until every instance sees maintenance mode before a restore applies

Status: accepted (ADR 48).

## Problem

ADR 38 decision 4 keeps the installation state in the database, so every
server instance reads the same value. Each instance caches the value for
`DefaultCacheLimit` (2 seconds). The restore apply step (ADR 38 decision 6)
replaces the live data in one step. When it applies while an instance still
sees `normal`, that instance serves tool calls against data that the apply
is replacing. The apply step must know that every instance can see
maintenance mode before it copies any data. ADR 38 leaves the design of that
wait open.

## Options

1. Wait twice the cache limit plus a clock-skew bound after the enter, then
   read the state again. With a 2 second limit and a 1 second skew bound,
   the apply starts 5 seconds after the enter. No new code paths, but the
   wait is fixed even when every instance already sees maintenance mode.
2. Ask every instance. Each instance exposes the state its watcher holds,
   and the apply step polls until all answer maintenance. This proves the
   state instead of waiting it out, but it needs a registry of instances and
   a new endpoint, and an instance that never answers blocks the restore.
3. Drop the cache. Every tool call reads the database. No wait is needed,
   but every tool call pays a database read, including the hot paths of the
   update cycle.

## Proposal

Option 1. The cache limit stays short, so a fixed wait of a few seconds is
cheap next to a restore, and it needs no registry of instances. The apply
step waits twice `DefaultCacheLimit` plus a 1 second clock-skew bound after
the enter returns, then reads the installation state once more through its
own store. When the read answers maintenance, it applies. When the read
answers anything else, the restore fails without applying, and the server
leaves maintenance mode.

## Open questions

- Is a 1 second clock-skew bound enough for installations whose instances
  run on different machines? The bound may need to come from settings.
- Must the wait survive a server restart between the enter and the apply?
  The restore record (ADR 38 decision 9) could hold the enter time, so a
  restarted apply step waits the remainder instead of the full bound.

## Owner direction (2026-10-12 and 2026-10-13)

The project owner answered this RFC on 2026-10-12 and 2026-10-13. Words in
quotation marks are the words of the owner. This section decides nothing. An
ADR records a decision (ADR 15).

- On the options: "I think I want instances to answer over the network."
  That is option 2.
- On discovery: a registry in the database with an advertised address.
- On the calls between instances: the public API with an administrator
  action.
- On an instance that does not answer: the owner asked how SWIM gossip
  handles it. The project lead proposed a lease rule. The owner said: "All
  RFCs are reviewed and I agree with the recommendations."
- On an instance that died permanently: the owner asked how it is removed,
  and approved the proposed rules: "Approved."
- On an instance that comes back: the project lead explained that an
  instance keeps no data of its own and that the fence refuses late writes.
  The owner answered: "So it's more of an issue when we set up async geo
  replication. Got it."

## Outcome

Accepted with option 2. [ADR 48](../adr/0048-instances-answer-over-the-network.md)
records the decision.

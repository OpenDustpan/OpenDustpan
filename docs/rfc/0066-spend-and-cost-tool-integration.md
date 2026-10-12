---
rfc: 66
title: Integration with spend and cost tools (FinOps)
status: draft
created: 2026-10-12
---

# RFC 66. Integration with spend and cost tools (FinOps)

Status: draft. The project owner asked for this RFC on 2026-10-12, in the
answers to RFC 45 and RFC 52. It decides nothing.

## Problem

RFC 45 asks which estimator OpenDustpan uses for a cost estimate on each plan,
and whether it is a recommended step or a built-in. RFC 52 lists, as a first
idea for the console, extension points for scanner and cost output without
running stored code in the browser. The owner said about that idea: "This is
too tool specific, and we should probably do a deeper dive in a separate RFC
first for integrating spend/cost OPS tools."

The question is wider than one estimator. Teams run FinOps tools to see what
their infrastructure costs. OpenDustpan holds the units, their labels and
their history. These tools need to learn about it, and OpenDustpan may need
to show what they know.

## First ideas

- The cost of a planned change, next to the plan (RFC 45).
- Cost data for a unit, shown in the inventory and the console (RFC 40,
  RFC 46, RFC 52).
- Labels on units for cost allocation, so that a FinOps tool can group spend by
  team or by namespace (RFC 4).
- Events for a cost tool to follow, such as a plan that finishes (RFC 23).
- A policy rule that reads a cost difference (RFC 13).

## Research task

The owner's answer to RFC 45 is: "Find out what competitors do, and do it
better." Find out:

- Which spend and cost tools the competitors integrate (RFC 45 names the
  products that show an Infracost estimate), and how: a job step, a webhook, a
  built-in, or an app of the vendor.
- What data goes in each direction, and in which shape.
- What users of those integrations ask for and complain about.
- What a self-hosted backend can do that a hosted product cannot, and the
  reverse.

Use primary sources: product docs and public repositories. Write the result as
a file in `docs/research/`, as the earlier research files do. Record each
claim with its source.

## Options

- **No integration.** OpenDustpan shows no cost data. A user connects a cost
  tool to the IaC code and to the runner by hand.
- **A recommended job step.** A runner job (RFC 6) runs an estimator. The
  server stores the result next to the plan.
- **A built-in estimator.** The binary includes one estimator. It costs code,
  and a dependency that needs the owner's approval.
- **Generic extension points.** The server accepts cost data through a
  documented API, from any tool. It stays tool-neutral (ADR 12). It costs the
  design of the data shape.

## Open questions

- (research) What do the competitors do, and what is better? See the research
  task above.
- Which direction of data matters first: cost data into OpenDustpan, or unit
  data out of it?
- Is the integration tool-neutral? It must not exist only because one cost
  tool needs it (ADR 12).
- How does a tool's cost data attach to a unit reference (ADR 13)?
- How does the console show cost output without running stored code in the
  browser (RFC 52)?
- How do RFC 45, RFC 46 and this RFC divide the work, so that one feature has
  one home?

## Outcome

Empty until the RFC is accepted (link the ADR) or dropped (say why).

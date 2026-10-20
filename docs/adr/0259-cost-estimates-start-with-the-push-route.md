# 259. Cost estimates start with the push route, and the first pull adapter comes later

Status: accepted · 2026-10-20. Decided by the project manager under the owner's delegation of 20 October 2026. Accepts part of RFC 45. Orders the two ways in of ADR 144 decision 5

## Context

Decided by the project manager under the owner's delegation of 20 October
2026, with the owner's decision rule.

ADR 144 decision 5 gives two ways in for a cost estimate of a plan:

- **Push.** A step of the role `tool` in a runner job runs an estimator
  and sends the result to `PUT /api/jobs/{id}/phases/{n}/cost-estimate`
  with its job token.
- **Pull.** An adapter of the `cost.Estimator` port reads the masked plan
  after the plan phase and answers the cost of each resource.

ADR 144 decision 6 puts no estimator in the binary, and says that a
dependency needs the approval of the owner. RFC 45 keeps the first adapter
open. The owner's direction on RFC 45 (2026-10-12) asks to find out what
other services do, and do it better.

Most estimators read prices from a price service. An adapter in the server
sends the resources of each plan of each namespace to that service. That
is a data flow out of the installation, and a regulated team must approve
it. A step in a runner job sends the plan only from the network and the
account of the team that chose the estimator.

## Decision

1. **Slice 1 is the push route.** The first slice of cost estimates ships
   the push route of ADR 144 decision 5, the store, the reads, the budget
   status and the facts of ADR 226. It ships no adapter of the
   `cost.Estimator` port.
2. **A guide, not a built-in.** The user documentation has one worked
   example: a step that runs an estimator on the plan JSON and sends the
   result. The example uses an estimator under an open source license, and
   names its price source and what the step sends to it.
3. **The first pull adapter is later.** RFC 45 keeps the choice of the
   first adapter. Its ADR must say which data leaves the installation, to
   which host, and how an operator turns it off. The adapter is off until
   an operator turns it on for a namespace. Its dependency needs the
   approval of the owner.
4. **No change to the gates.** A gate on cost reads the facts of ADR 226.
   The `cost` facts are `available == false` when no estimate arrived, and
   a gate that reads them fails closed (ADR 226 decision 3).

Benchmark: AWS and Google Cloud price their own resources and send no
customer plan to a third party. PCI DSS and ISO 27001 ask for a record of
each data flow to a third party. The push route keeps that flow in the
network of the team, under its own choice.

## Consequences

- A team with runners gets estimates in its plans, from the estimator that
  it chooses. A team with no runner gets no estimate in slice 1.
- The server makes no call to a price service. An air-gapped installation
  loses nothing.
- The pull adapter is one more piece of work, after an ADR that names its
  data flow.

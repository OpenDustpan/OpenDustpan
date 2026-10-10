---
rfc: 31
title: Kubernetes runner and operator integrations
status: draft
created: 2026-10-10
---

# RFC 31. Kubernetes runner and operator integrations

Status: draft. A stub opened from the competitor review in the research doc
"IaC and TACOS landscape". It records the gap and first ideas only.

## Problem

Teams that run Kubernetes manage everything as resources in the cluster. The
Pulumi Kubernetes Operator, tofu-controller and Burrito run jobs as pods and
keep state elsewhere: the operator accepts any Pulumi backend URL, and
tofu-controller and Burrito accept a remote OpenTofu backend. None has a
console or history.

## First ideas

- A first-party Kubernetes runner for RFC 6: one pod per job, a pod template,
  a service account per unit.
- Test in e2e that the Pulumi Kubernetes Operator, tofu-controller and Burrito
  can use OpenDustpan as their backend.
- Optional: custom resources for units, so that Flux or Argo CD can manage
  them, and outputs written to a Secret (tofu-controller).

## Open questions

- Is an operator in scope for this repository, or a separate project?

## Outcome

None yet.

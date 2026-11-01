# 307. OpenTofu only, uploaded plans, TFE run fields and run triggers

Status: accepted · 2026-11-01. Records decisions of the project owner of 18 October 2026 that no ADR held

Changes:

- Extends ADR 25 decision 1: the HashiCorp Terraform CLI is out of scope.
- Extends ADR 267 decision 8: the TFE-compatible view of run triggers.
- Extends ADR 102: a plan that a team uploads from its own CI.

## Context

The project owner made these decisions in the owner decision file of 18 October 2026. A check on 1 November 2026 found no ADR that states them.

ADR 25 says that the Terraform CLI stays untested. ADR 267 decision 8 puts the TFE-compatible view of run triggers in a separate issue. ADR 102 binds a runner plan to a plan hash.

This ADR writes the words of the owner. It adds no decision of its own.

## Decision

1. **The HashiCorp Terraform CLI is out of scope (owner, 2026-10-18).** The owner said: "HashiCorp Terraform CLI: out of scope". The owner said also: "only OpenTofu is supported and tested".
2. **Own-CI teams may upload plans (owner, 2026-10-18).** The owner said: "Own-CI teams: accept uploaded plans". The owner said also: "the apply must present the same plan hash". The owner said also: "weaker than a runner plan, and marked so in the change order".
3. **Run triggers are a view of our triggers (owner, 2026-10-18).** The owner said: "TFE `run-triggers`: served, as a view of our unit-follows-unit triggers".

## Consequences

- ADR 25, ADR 102 and ADR 267 get a pointer to this ADR.
- The endpoint of decision 3 lands with its e2e step first, against the real OpenTofu CLI.
- The row D-018 (workspace execution mode and agent pool fields writable, owner 2026-10-18) is MOOT. ADR 254 (accepted 2026-10-20) is later and decides the execution mode. This ADR does not record the row.

# 307. OpenTofu only, uploaded plans, TFE run fields and run triggers

Status: accepted · 2026-11-01. Records decisions of the project owner of 18 October 2026 that no ADR held

Changes:

- Extends ADR 25 decision 1: the Terraform CLI is not supported. OpenDustpan supports and tests OpenTofu.
- Extends ADR 254 decision 2: the workspace execution mode and agent pool fields are writable.
- Extends ADR 267 decision 8: the TFE-compatible view of run triggers.
- Extends ADR 102: a plan that a team uploads from its own CI.

## Context

The project owner made these decisions in the owner decision file of 18 October 2026. A check on 1 November 2026 found no ADR that states them.

ADR 25 says that the Terraform CLI stays untested. ADR 254 makes the execution mode of a workspace settable. It says also that the TFE agent pool API is not served. ADR 267 decision 8 puts the TFE-compatible view of run triggers in a separate issue. ADR 102 binds a runner plan to a plan hash.

This ADR writes the words of the owner. It adds no decision of its own.

## Decision

1. **The Terraform CLI is not supported (owner, 2026-10-18).** OpenDustpan supports and tests OpenTofu. The owner said: "only OpenTofu is supported and tested".
2. **Own-CI teams may upload plans (owner, 2026-10-18).** The owner said: "Own-CI teams: accept uploaded plans". The owner said also: "the apply must present the same plan hash". The owner said also: "weaker than a runner plan, and marked so in the change order".
3. **The execution mode and agent pool fields are writable (owner, 2026-10-18).** The owner said: "TFE workspace execution mode and agent pool fields: writable". The owner said also: "same authorization as the tool-neutral route". The owner said also: "both map to one rule".
4. **Run triggers are a view of our triggers (owner, 2026-10-18).** The owner said: "TFE `run-triggers`: served, as a view of our unit-follows-unit triggers".

## Consequences

- ADR 25, ADR 102, ADR 254 and ADR 267 get a pointer to this ADR.
- Each endpoint of decisions 3 and 4 lands with its e2e step first, against the real OpenTofu CLI.
- Open for the project manager: ADR 254 says that the TFE agent pool API is not served. Decision 3 makes the agent pool field of a workspace writable. The two texts may need one reading.

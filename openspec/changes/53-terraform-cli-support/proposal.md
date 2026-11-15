---
version: 2
---

## Why

**Persona: developer.** A Developer who must use the Terraform CLI tries to keep state, locks and history in OpenDustpan like an OpenTofu workspace, but the roadmap keeps this item behind OpenTofu and a license check.

The user path this change must make work:

1. **CLI.** Developer points the Terraform CLI at OpenDustpan and runs init, plan and apply.

Evidence for the claims above:

- This item waits on OpenTofu and a license check. Proof: | [RFC 53](rfc/0053-terraform-cli.md) Terraform CLI support                                          | OpenTofu (milestone 2) and a license check                      |
- The e2e suite drives OpenTofu through a helper. Proof: e2e/tool.go:110 (func OpenTofu)

## What Changes

- Add nothing now: record that Terraform CLI support waits on OpenTofu (milestone 2) and a license check.
- Refine the RFC with tool scope, protocol surface and license limits when the gate passes.

Out of scope:

- Any change to the OpenTofu workspace model
- Runners, jobs, profiles and inventory queries

Depends on:

- none

Builds on (not a change):

- OpenTofu support (milestone 2)

## Usability

Where the path still costs the person time or surprises them:

- A Terraform CLI person cannot tell yet which commands will work or which versions are allowed.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The e2e suite drives OpenTofu through a helper. (`e2e/tool.go:110 (func OpenTofu)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 13: Separate tool models; shared access control and inventory** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 19 extends; ADR 41 changes; ADR 150 changes; ADR 247 extends; ADR 265 extends; ADR 337 extends. Chain to the end: ADR 19, ADR 41, ADR 150, ADR 247, ADR 265, ADR 337, ADR 47, ADR 248, ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 154, ADR 199, ADR 210, ADR 242, ADR 153, ADR 346, ADR 286, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299.
- Current rule: this ADR, with ADR 19, 41, 150, 247, 265, 337.
- Quote: "Separate write models:

- Each tool keeps its own domain model, state machine and storage schema
  (`stack` for the Pulumi CLI, `workspace` for OpenTofu), as ADR 12 requires."
- Effect: A Terraform CLI tool keeps its own domain model and storage apart from the workspace model, and shares only namespaces, access control and inventory.
- Conflict: none.

**ADR 41: The server reads state and masks secrets** (not owner-tagged).

- Decisions that apply: 1 (current).
- Changed by: ADR 60 changes decisions 5 and 13; ADR 103 changes decision 13; ADR 146 extends decision 13; ADR 151 changes decision 10; ADR 238 changes decision 12; ADR 240 changes decisions 9 and 13; ADR 240 extends decision 5; ADR 352 extends decision 10. Chain to the end: ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 210, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 149, ADR 262, ADR 297, ADR 154, ADR 217, ADR 220, ADR 315, ADR 356, ADR 354, ADR 124, ADR 216.
- Current rule: this ADR, with ADR 60, 103, 146, 151, 238, 240, 352.
- Quote: "The server may read state. The server may read the contents of every state that it stores, for each tool."
- Effect: Any stored Terraform state follows the same read rule as other tools: each server read needs a declared action on a unit.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does the license check permit support for the Terraform CLI, and under what version and protocol limits?
   - Options: Build Terraform CLI support after a passing license check; Decline Terraform CLI support on license grounds.
   - Provisional decision (owner review 14 Nov): Ask the owner for a license decision with named allowed versions before any design work.
   - Reason: The roadmap row makes the license check an explicit gate, and no ADR in the pack answers it. Benchmark: AWS: verify service terms and license terms before building protocol compatibility, and publish only supported tool versions.
   - Persona impact: A Developer who needs the Terraform CLI stays blocked until decided; OpenTofu and Pulumi CLI persons are not hurt. Escape hatch: If refused, keep OpenTofu only and document that the Terraform CLI is not supported.
2. How does the server tell the Terraform CLI apart from OpenTofu at the shared discovery document?
   - Options: Serve a separate discovery path for the Terraform CLI; Keep one discovery path with explicit version negotiation; Decline Terraform CLI support.
   - Provisional decision (owner review 14 Nov): Use explicit version negotiation with a closed allowlist and refuse closed on mismatch. Do not trust the user agent.
   - Reason: The pack proves a shared discovery route exists but gives no detection rule. Benchmark: AWS: gate protocol access by declared client capabilities and version negotiation, and refuse unknown clients closed.
   - Persona impact: A Developer sees a clear unsupported-tool error either way; no other persona is hurt. Escape hatch: If reliable detection is not possible, keep OpenTofu only.
3. Does the Terraform CLI share the TFE wire adapter with OpenTofu or get its own adapter, and which lock, unlock, state and masking flows must each cover?
   - Options: One TFE wire adapter with two domain models behind it; A separate Terraform adapter and domain.
   - Provisional decision (owner review 14 Nov): Keep one wire adapter only if the RFC proves version parity for lock, state and masking; else split the adapter.
   - Reason: ADR 13 requires separate domain models but does not decide wire-adapter sharing. Benchmark: PCI DSS: isolate each tool data model and mask sensitive values in every read path.
   - Persona impact: A Developer gets the same init, plan, apply, lock, unlock, state pull and push path either way. Escape hatch: If sharing is unsafe, build a separate Terraform adapter and domain.

Questions considered and answered:

- Can Terraform CLI reuse the OpenTofu workspace model? No. ADR 13 requires a separate domain model and storage schema for each tool.
- Does the license check permit Terraform CLI interoperation? Not answered by any ADR in the pack. It stays an open question for the owner.
- Can one TFE wire adapter serve both tools with two domain models? Not answered. ADR 13 separates domain models but says nothing about sharing a wire adapter.
- Which route and action rules apply to a new Terraform CLI path? The pack gives no decision text for them. Refinement of the RFC must cover them.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

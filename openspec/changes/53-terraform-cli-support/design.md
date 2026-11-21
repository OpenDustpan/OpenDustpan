## Context

A developer who must use the Terraform CLI tries to keep state, locks, and history in OpenDustpan like an OpenTofu workspace. The roadmap keeps this item behind OpenTofu (milestone 2) and a license check.

This change records that Terraform CLI support waits on that gate. It adds nothing now and changes no server code, no route, and no schema.

## Decisions

1. **No code now.** The Terraform CLI path ships only after OpenTofu lands and the license check passes. No protocol surface lands before the gate.
2. **The OpenTofu workspace model stays unchanged.** Terraform support rides the workspace path the e2e suite already drives; no second workspace model appears for one CLI.
3. **Refine tool scope, protocol surface, and license limits at the gate.** The later refinement names exactly which CLI operations the server accepts and which license terms bound them. Anything outside that scope is refused with a named error.

## Structure

No new structure now. The bug class is a Terraform-only state path beside the OpenTofu workspace model; one workspace model for both CLIs is the structure that removes it. When the gate passes, CLI acceptance is an allowlist value built only through its constructor, which refuses operations outside the refined scope.

No custom analyzer and no new tool. The license check needs the owner.

## Risks

- Terraform users wait until milestone 2 and the license check pass. The docs page says OpenTofu is the supported CLI until then.
- A license term forbids a planned operation. The refinement names the license limits first, so no code depends on a forbidden path.

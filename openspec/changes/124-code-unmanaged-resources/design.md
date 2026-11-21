## Context

A developer finds an unmanaged resource in the inventory and wants starter code that brings it under management in a stack or workspace, without hand-writing every block from the cloud state.

This change records RFC 124 as a coarse Later change with no behavior yet: slices 0 to 6 wait on the sweep plugin milestone (RFC 123), slices 7 and 8 wait on the agents milestone (milestone 10). It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** Code output for unmanaged resources ships only after discovery and codification (from 47-discovery-codification) plus the sweep plugin milestone land. No generator lands before there are findings to generate from.
2. **Sweep schedules, targets, and findings stay out.** They refine with the sweep plugins, not here. This change covers code output only.
3. **Generated code never carries plain secrets.** Discovery reads mask secrets, and code output keeps the markers. Reveal stays a separate call.
4. **Reads name the version check.** A read of managed resources for a version that does not exist answers 404 with the error code for an unknown state version and names the check for the version.

## Structure

No new structure now. The bug class is generated code that the stack cannot plan with or that leaks a secret; generating from swept findings with masked values is the structure that removes it. When the gates pass, generated code is a value built only through its constructor, which checks the finding reference and refuses secret-shaped values.

No custom analyzer and no new tool.

## Risks

- Developers hand-write blocks until the sweep and agents milestones land. The guide page ships with the refinement and names its limits.
- Generated code drifts from the cloud state it was built from. The output names the finding and its time, so a re-sweep shows what changed.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| domain | Refuse secret-shaped values in generated code; keep masking markers from discovery reads. | Generate code for a resource that holds a secret; the output shows the marker only. |
| handler | Answer an unknown state version with 404 and the named code; name the version check. | Read resources of a version that does not exist; the answer carries the code, not a stack trace. |

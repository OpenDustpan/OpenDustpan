## Context

A developer with existing cloud resources tries to find unmanaged resources and bring them under management, but discovery and codification wait on runs and have no accepted design yet.

This change records RFC 47 discovery and codification as waiting on runs in milestone 5. It refines scope, sources, and code output only after RFC acceptance. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** Discovery sources and codification output ship after runs land. Scope, sources, and code output stay open until the RFC is accepted.
2. **Run model, runners, and triggers stay out.** They ship in milestone 5 on their own path. Discovery reads the results; it does not build the execution path.
3. **Code generation detail stays in RFC 124.** This change covers finding and scoping unmanaged resources; starter code refines from 124-code-unmanaged-resources.
4. **Findings never carry plain secrets.** Discovery reads mask secrets, and any codified output keeps the markers. Reveal stays a separate call.

## Structure

No new structure now. The bug class is a finding that names a resource the stack cannot adopt; scoping discovery to sources the run record can name is the structure that removes it. When the RFC is accepted, a finding is an unexported value built only through its constructor, which checks the source, the resource reference, and the masked form.

No custom analyzer and no new tool.

## Risks

- Developers wait for discovery until runs land. Manual import paths stay the workaround until then.
- A finding points at a resource that moved. The finding names its source and time, so a re-run shows what changed.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| domain | Mask secret values in each finding; keep reveal on its own call with its own grant. | Discover a resource that holds a secret; the finding shows the marker only. |
| service | Record the source and time on each finding; fail closed on an unknown source. | Post a finding from an unknown source; the server refuses it. |

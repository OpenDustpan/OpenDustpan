## Context

An operator installs and configures the server and wants to manage groups, roles, grants, and settings as code, the same way each change is scripted and reviewed, instead of by console acts alone.

This change names the first objects under code management after the groups and roles API is stable (milestone 4). It defines the declarative file form, plan, preview, apply, and audit record in a later refinement. It adds no routes, moves no units, and changes no tool behavior now.

## Decisions

1. **No code now.** The file form, plan, preview, and apply ship after the groups and roles API is stable. No code-management path lands before the API it drives.
2. **Access and settings only.** Groups, roles, grants, and settings go under code management first. Secrets, Pulumi stacks, and OpenTofu workspaces stay out, as do unit moves between namespaces and installations.
3. **The stable API is the only actuator.** Preview and apply call the REST API; no second write path appears beside it. The capabilities document names the API version, and an apply against an unknown version fails with the stable error and changes nothing.
4. **Weaker settings need a fresh authentication.** An apply of a weaker setting or claim rule without a fresh step fails with 403 and `mfa.fresh-step-required`. An apply that removes the last enabled break-glass principal fails with 409. An apply without the needed action on the object fails with 403. Each failure changes nothing.
5. **Kubernetes operator overlap is split cleanly.** The ADR 305 operator declares units; this change declares access and settings. Neither declares the other's objects.

## Structure

No new structure now. The bug class is a code-managed apply that bypasses the API grant checks; driving every apply through the stable REST API with per-object action checks is the structure that removes it. When the API is stable, a managed declaration is an unexported value built only through its constructor, which checks the API version, the object actions, and the fresh-authentication rule for weaker settings.

No custom analyzer and no new tool.

## Risks

- Operators keep clicking through the console until the API is stable. Console acts keep working and keep their audit records.
- An apply removes the last break-glass principal and locks out recovery. The 409 guard refuses it, and the plan names the break-glass rule before apply.
- A stale file weakens a setting without review. The fresh-authentication rule plus the plan preview force a conscious, signed-in confirmation.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| handler | Check the needed action on each object before any apply; refuse weaker settings without a fresh authentication with 403 and `mfa.fresh-step-required`. | Apply a weaker setting with an old session; the answer is 403 and nothing changes. |
| service | Refuse an apply that removes the last enabled break-glass principal with 409. | Apply a file with no break-glass principal; the answer is 409 and the principals are unchanged. |
| domain | Resolve grants by stable ID; an unknown API version fails the apply with the stable error. | Apply against an unknown API version; the answer is the stable error and nothing is stored. |
| store | Carry the fence and the maintenance check on each managed write. | Write a grant row without a fence; the store refuses it. |

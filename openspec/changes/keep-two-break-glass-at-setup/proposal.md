---
version: 1
---

## Why

**Persona: site administrator.** As a site administrator I must not finish the first setup of an installation with fewer than two break-glass accounts, so the first loss of the identity provider cannot lock every administrator out.

The user path this change must make work:

1. **API.** The site administrator adds two break-glass accounts with the add route of the change keep-two-working-break-glass, then finishes the first setup.
2. **API.** The site administrator tries to finish the first setup with fewer than two working break-glass accounts and gets 409 with the missing count.
3. **E2e.** The shared e2e first setup makes two break-glass accounts, so each other e2e step still starts from a finished setup.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| First-setup finish with fewer than two break-glass accounts. | 409 with code break-glass.setup-incomplete and the missing count. | `make e2e` |

Evidence for the claims above:

- Main has no setup-finish flow, so the gate has no place to stand yet. Proof: the change keep-two-working-break-glass found no such flow on main, and its first-setup task moved here.

## What Changes

- A first-setup gate on two working break-glass accounts with a passkey or a hardware key, using the count function of the change keep-two-working-break-glass. The shape of the finish flow waits on its own design.
- The shared e2e first-setup step makes two break-glass accounts.

Out of scope:

- The count, the guard, the list and add routes, the restore check and the rotation (change keep-two-working-break-glass).

Depends on:

- keep-two-working-break-glass

## Usability

Where the path still costs the person time or surprises them:

- The refusal names the missing count and the add route of the change keep-two-working-break-glass.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main counts working break-glass accounts and keeps the floors as constants. This change reads the count at setup finish. (`pkg/principal/breakglass_count.go:18 (const FloorWorkingPaths)`)

## ADR check

ADR 70 decision 24 (not owner-tagged) says that the first setup cannot finish without two break-glass accounts. The owner decision of 13 Nov 2026 (queue P19, item 17) keeps it: "first setup needs two". Conflict: none. The refinement quotes the ADR text from docs/adr/ when it becomes a full change.

## Open questions

none

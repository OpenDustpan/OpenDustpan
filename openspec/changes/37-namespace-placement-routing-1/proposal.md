---
version: 3
---

## Why

**Persona: operator.** An Operator runs more than one installation and must know which installation owns each namespace. A Developer who reaches the wrong installation gets a plain 421 and the one next step, not a silent write in the wrong place.

The user path this change must make work:

1. **API.** Operator reads the primary installation ID of a namespace from the settings resource of the namespace, with an access token that has a read grant.
2. **CLI.** Developer runs the Pulumi CLI or OpenTofu against an installation.
3. **API.** A write that reaches a non-primary installation answers 421 with the primary installation ID. The caller retries against the primary.
4. **Docs page.** Operator reads the placement rule on the NEW docs page docs/reference/namespace-placement.md.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| A write to a namespace on an installation that is not its primary installation fails. | 421 with the NEW error code `federation.not-primary`. The detail names the primary installation ID and nothing else of the input. | `curl -H "Authorization: Bearer $TOKEN" https://<installation>/api/dustpan/v1/namespaces/{namespace}/settings` |

Evidence for the claims above:

- Each installation has namespace settings routes. Proof: Route table lists ANY/GET/PATCH /api/dustpan/v1/namespaces/{}/settings
- Today only the Pulumi adapter maps a write outside the primary installation to 421. Proof: docs/rfc/0037-federation.md: Only the Pulumi adapter maps it to 421 (`internal/api/pulumi/pulumi.go`). The TFE-compatible adapter and the OpenTofu http adapter have no mapping for it.

## What Changes

- Show one primary installation ID for each namespace in the effective settings of the namespace, read-only.
- Set the primary installation ID in the create transaction of a namespace, to the ID of the installation that creates it.
- Answer 421 with the NEW error code `federation.not-primary` for a namespace write on a non-primary installation, on the Pulumi, TFE-compatible and OpenTofu paths.
- Check the primary inside the write transaction, with the namespace.Fence.
- Serve reads locally on any installation.
- Document the placement rule and the 421 shape.

Out of scope:

- Forwarding a request to the primary installation.
- Move of a namespace to another installation, with its authorization and failure handling (37-namespace-placement-routing-move).
- Change of the primary installation by a write.
- Asynchronous replication of namespace data.
- Installation settings storage (68-installation-settings).

Depends on:

- 68-installation-settings

## Usability

Where the path still costs the person time or surprises them:

- The 421 names the primary installation ID, not its address, so the Operator needs one lookup until installation settings hold an address.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Namespace settings routes exist under /api/dustpan/v1/namespaces/{namespace}/settings. (`internal/api/server/githost.go:115 (func Handler.gitHostNamespaceRoutes)`)
- The installation settings type exists in the agent package. (`internal/agent/settings.go:35 (func DefaultInstallationSettings)`)
- The default installation settings constructor exists on main. (`internal/agent/settings.go:52 (func DefaultInstallationSettings)`)

## Owner decisions that apply

The project owner approved these decisions on 13 Nov 2026 ("these are all approved as written"). They bind this change. Each is quoted as written.

- **Owner decision, 13 Nov 2026 (queue P5)**, Batch 1: default collection at namespace create: "B. A new namespace gets a stored, visible, audited `default` collection and a catch-all placement rule in the create transaction; an admin can delete either; the create request can opt out. Reason: works first time from the docs (Terraform Cloud "Default Project"); "no match, no unit" (ADR 51 d5, ADR 61 d3) still holds because the rule is explicit and stored."

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 4: Namespaces, home regions and failover-version fencing** (not owner-tagged).

- Decisions that apply: the ADR as a whole.
- Changed by: ADR 38 extends; ADR 248 changes; ADR 286 changes. Chain to the end: ADR 38, ADR 248, ADR 286, ADR 42, ADR 48, ADR 70, ADR 151, ADR 210, ADR 212, ADR 253, ADR 72, ADR 142, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 369, ADR 154, ADR 217, ADR 220, ADR 247, ADR 232, ADR 235, ADR 301, ADR 365, ADR 73, ADR 75, ADR 103, ADR 126, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 149, ADR 297.
- Current rule: this ADR, with ADR 38, 248, 286.
- Quote: "Each namespace is authoritative in exactly one region, its home region."
- Effect: Keeps one authority for each namespace. ADR 248 renamed the home region to the primary installation, and this change uses that word.
- Conflict: none.

**ADR 248: Federation model: installations own namespaces, request forwarding, asynchronous replication and manual failover** (not owner-tagged).

- Decisions that apply: 6 (current), 14 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "The fence stays. Each write in a namespace carries a `namespace.Fence`, and the store checks it in the same transaction, as ADR 4 decides."
- Quote: "Background work runs on the primary installation."
- Effect: The primary check runs in the write transaction with the fence. Background work stays on the primary installation.
- Conflict: none.

**ADR 286: Self-service, failover and promotion: three renames** (not owner-tagged).

- Decisions that apply: 7 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Other uses of "promote" in the code change (PM decision under the owner's delegation)."
- Effect: The text says failover for a change of primary installation, never promote.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does this part forward a request to the primary installation? No. ADR 4 keeps 421 for a write outside the primary, and forwarding is later work.
- Does a non-primary installation run background work for the namespace? No. ADR 248 decision 14 keeps it on the primary installation.
- How does a new namespace get its primary ID? The create transaction sets it. Owner decision, 13 Nov 2026 (queue P5) adds a stored default collection and a catch-all rule in the same transaction. Neither touches the primary ID.
- Can PATCH on settings change the primary? No. The field is read-only here. A write to it answers the existing validation error. A change belongs to the move part.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

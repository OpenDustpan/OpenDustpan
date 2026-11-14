---
version: 2
---

Roadmap: `docs/roadmap.md:315` (Output sharing).

## Why

**Persona: developer.** Developer runs the Pulumi CLI and OpenTofu against one server and reads outputs of a unit of the other tool from a runner job. With no clear rule, the read fails or leaks across units. This change lets each job read only what a producer rule and the starter grants allow.

The user path this change must make work:

1. **API.** Read the sharing rule of the producer that names the consumer unit, collection, or namespace.
2. **CLI.** Run pulumi preview or tofu plan on a runner where the code reads outputs of the other tool unit.
3. **Console.** Read the plan log and the linked producer state outputs.
4. **Docs page.** Follow docs/guides/output-sharing.md for both tool reads.
5. **E2e.** Drive the real CLIs of the matrix through e2e/output_sharing_test.go, with the 403 path for a missing rule.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Job read of another unit outputs with no naming rule gives 403. | 403 with error code unit.forbidden. List the units of the producer namespace to check names, then read the guide. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| Job read of a secret output when the starter lacks read-secrets gives 403. | 403 naming unit:read-secrets and the 403 shape in docs/guides/output-sharing.md. Retry the same outputs read after a grant change to confirm the fix. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/outputs` |
| Job read of a producer with no latest state gives 404. | 404 with error code unit.not-found. Check the producer name and run the read again after its first state write. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |

Evidence for the claims above:

- An e2e file covers output sharing. Proof: e2e/output_sharing_test.go
- A guide describes sharing rules, producers, and consumers. Proof: docs/guides/output-sharing.md
- ADR 273 defines the read-outputs action and producer-held sharing rules. Proof: docs/adr/0273-output-sharing-rules-and-the-read-outputs-action.md

## What Changes

- Enforce producer sharing-rule plus starter-grant checks on each cross-tool outputs read
- Serve Pulumi reads of OpenTofu outputs and OpenTofu reads of Pulumi outputs through the covered output routes
- Deny with 403 and audit when no rule names the consumer or the starter lacks unit:read-secrets for secret outputs

Out of scope:

- Sharing-rule storage, validation, list, update, delete, and the output-sharing setting for new units and one-step setup
- Uploaded plans from team CI
- Gate simulation and gate configuration routes

Depends on:

- add-output-sharing-setting-new
- `add-output-sharing-setting-new` (order of the milestone review)
- `job-service-unit-settings-pool` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A Developer learns of a 403 only at job run time when the producer saved no rule.
- Rule setup names stable unit and collection IDs, and copy must show stack and workspace names with tool and namespace.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Built-in roles hold output sharing actions (`pkg/access/access_test.go:1723`)
- Custom role with unit read only does not read outputs (`pkg/access/access_test.go:1778`)
- Grant check names subject and namespace (`pkg/access/access_test.go:783`)
- Read refuses job starter subject without principal (`pkg/jobtoken/jobtokentest/mem_test.go:671`)
- Reveal denies without read-secrets (`internal/api/pulumi/reveal_test.go:510`)
- Apply steps refuse hostile Pulumi names (`pkg/dustpan/jobservice_applysteps_internal_test.go:120`)
- Every audit event has a producer (`pkg/runner/handoff_test.go:158`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 273: Output sharing: the read-outputs action and sharing rules on the producer** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current), 3 (current), 5 (current), 6 (current), 7 (current), 8 (current), 11 (current), 12 (current).
- Changed by: ADR 286 changes. Chain to the end: ADR 286.
- Current rule: this ADR, with ADR 286.
- Quote: "A separate action reads outputs (15.1, option a). The new tool-neutral action `unit:read-outputs` allows a read of the root outputs of the latest state of a unit, and nothing else. Its object is a unit, a collection or a namespace. A collection and a namespace widen the object (ADR 51 decision 8). A secret output also needs `unit:read-secrets`, as in ADR 213 decision 1. `unit:read` does not imply `unit:read-outpu"
- Quote: "The producer holds the sharing rule. The producer is the unit that makes the outputs. A rule is an object of the producer namespace. Its producer is a unit, a collection or a namespace. Its consumer is a unit, a collection or the namespace of the producer. The unit that makes the outputs decides who reads them. A rule gives a job of a consumer unit the right to read. A rule gives a person nothing. A person reads"
- Quote: "A rule across namespaces names units or a collection (15.4, option b). A rule can name a unit or a collection of another namespace as its consumer. It never names a whole other namespace. It never names a principal of any namespace. The producer decides alone: the rule needs no acceptance by the other namespace. The other namespace only gains a read, and the namespace that gives its outputs is the one that agrees"
- Quote: "**A job token reads only what a rule names and the starter can read (15.5, option c).** A job token reads the outputs of another unit only when both checks pass at each read: - Check 1: a sharing rule of the producer names the unit of the job, its collection or its namespace. - Check 2: the principal that started the job holds `unit:read-outputs` on the producer with its own current grants, and `unit:read-secrets"
- Quote: "**A decrypt for a caller that only reads outputs decrypts only outputs (15.2, option a).** For the Pulumi CLI, a secret output reaches the consumer as ciphertext of the producer, and the CLI asks the producer to decrypt it. A job token of another unit that passes both checks of decision 5 can decrypt only ciphertext that the root outputs of the latest checkpoint of the producer hold. - A principal decrypts only w"
- Quote: "The action covers only the paths that read outputs (15.3, option a). `unit:read-outputs` covers only a route that answers outputs and nothing else: - the tool-neutral route `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/outputs`, - the TFE-compatible routes `current-state-version-outputs` and `state-version-outputs/{id}`, - the read of a workspace that `tfe_outputs` makes, when a slice serves it, - t"
- Quote: "Defense in depth (ADR 250). Each layer stops a bad read alone: - The HTTP adapter checks the request with an allowlist of object kinds. - The store runs `Rule.Validate` at each write. It checks that each unit and each collection of the producer namespace that a rule names exists with its stable ID. It binds `Get` and `Delete` to the producer of the route, so that `unit:manage-sharing` on one unit does not reach a"
- Effect: This change enforces only the cross-tool read check: a naming rule plus unit:read-outputs of the starter plus unit:read-secrets for a secret output at each read. Rule storage, validation, and the new-unit default live in add-output-sharing-setting-new.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Who decides that a job of another unit reads outputs? ADR 273 decision 2 answers it: the producer decides alone, a rule gives a job of a consumer unit the right to read and gives a person nothing.
- What checks govern a cross-tool read of outputs? ADR 273 decisions 1 and 5 answer it: a naming rule plus unit:read-outputs of the starter at each read, plus unit:read-secrets for a secret output.
- Which routes serve cross-tool output reads? ADR 273 decisions 7 and 8 answer it: the tool-neutral outputs route, the named TFE-compatible output routes, and the Pulumi read of OpenTofu outputs through terraform_remote_state for e2e-driven versions.
- Where does sharing-rule management live? Rule storage, validation, list and delete live in add-output-sharing-setting-new; this change only reads stored rules by stable ID at check time.
- What happens when the audit write fails during a read or denial? Fail closed per project security rule. If the audit write fails the read fails with 500 and no outputs leave the server.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

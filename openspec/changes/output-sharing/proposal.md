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
- job-service-unit-settings-pool
- `add-output-sharing-setting-new` (order of the milestone review)
- `job-service-unit-settings-pool` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A Developer learns of a 403 only at job run time when the producer saved no rule.
- Rule setup names stable unit and collection IDs, and copy must show stack and workspace names with tool and namespace.

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

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): The Open questions section reads not spec-ready. Two routes named in the error table, GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/sharing-rules and GET /api/dustpan/v1/me/reach, are not in the API description or the route table, so the one command that helps does not exist. File: proposal.md. Resolution: answered: Replaced both one-commands with routes from the route table: list units and the tool-neutral outputs read. No new route is named in the error table.
- R1.2 (missing scenario): The spec has no requirement or scenario for saving, reading or deleting sharing rules, although proposal step 1 and task 2 depend on them. The 404 unit.not-found path for an unknown producer or consumer ID, and the store Validate and Get/Delete binding from design layer 3, are also not specified. File: specs/output-sharing/spec.md. Resolution: answered: Added rule-manage requirement with save, read, delete scenarios, 404 for unknown stable ID, and producer binding.
- R1.3 (open question): ADR 273 decision 6 (decrypt only outputs of the latest checkpoint for a caller that passes both checks) is not in the spec. The spec only says secret values are sealed for the authorized path, which leaves the decrypt rule and its checks undefined. File: specs/output-sharing/spec.md. Resolution: answered: Added decrypt-only-outputs requirement with latest-checkpoint and hostile-ciphertext scenarios.
- R1.4 (failure mode): The design risks revoked starter grants and removed rules between job start and read, and the layer table says the in-flight read is denied with 403. The spec has no scenario for revocation, rule removal mid-job, or a race between a rule delete and a read. File: specs/output-sharing/spec.md. Resolution: answered: Added revocation mid-job requirement with removed-rule and revoked-grant scenarios.
- R1.5 (missing scenario): The proposal promises a denial audit entry and the design hostile tests cover a namespace-kind consumer and a principal-name consumer, but the spec has no scenario for either hostile input. The spec also has no audit requirement for the OpenTofu success read, although the project rule requires a record for each automatic action. File: specs/output-sharing/spec.md. Resolution: answered: Added hostile-kind and audit requirements covering namespace-kind and principal-name consumers and the OpenTofu audit entry.
- R1.6 (missing scenario): Decision 8 requires that a Pulumi read of OpenTofu outputs runs through the terraform_remote_state routes and that only e2e-driven CLI versions are supported. The spec's covered-routes requirement omits these routes and any version gate. File: specs/output-sharing/spec.md. Resolution: answered: Added terraform_remote_state route coverage and e2e-driven version gate to the covered-routes requirement.
- R1.7 (scope): Task 2.2 wires only the tool-neutral and TFE outputs routes, but the change claims Pulumi reads of OpenTofu outputs, which need the terraform_remote_state path of ADR 273 decision 8. The task list and the spec do not match the claimed user path. File: tasks.md. Resolution: answered: Build task now wires the tool-neutral, TFE-compatible, and terraform_remote_state paths together.
- R1.8 (open question): The ADR check lists decisions 1, 2, 3, 5, 7, 8, 10 and 11 but omits decisions 4 (newUnitDefault), 6 (decrypt) and 12 (defense in depth), which the change touches. Decision 3 (no namespace-wide consumer across namespaces) is not reflected in the spec's consumer scope. File: proposal.md. Resolution: answered: Extended adr_check to decisions 1,2,3,4,5,6,7,8,10,11,12 with quotes for cross-namespace scope, new-unit default, decrypt, and defense in depth.
- R1.9 (failure mode): The design layer 1 guard requires an allowlist of producer and consumer object kinds, but the spec does not say how a read behaves when the job token has no attachment for the profile block, or when the producer unit is renamed or removed and made again. Stable-ID behavior is not specified. File: specs/output-sharing/spec.md. Resolution: answered: Added stable-ID requirement for rename, remove-and-make-again, and missing job attachment.
- R1.10 (wrong claim): The proposal's evidence says the roles guide documents the read outputs path (docs/guides/roles-and-grants.md), but the change's tasks do not include updating that guide, and no docs task covers the 403 error shape named in the proposal table. File: proposal.md. Resolution: answered: Removed the roles-guide claim and made the docs task cover the 403 error shape and the one commands.
- R2.1 (failure mode): specs/output-sharing/spec.md: no requirement says what happens when the audit write fails during a successful read or a denial. The project rule says nothing happens without a record, so the read must be refused or its outcome defined. Resolution: answered: Added fail-closed audit requirement: when the audit write fails the server fails the read with 500 and returns no outputs.
- R2.2 (failure mode): specs/output-sharing/spec.md: the recheck at each read does not cover a rule delete or grant revoke that lands between the check and the response or decrypt of an in-flight read. The time-of-check to time-of-use window is not specified. Resolution: answered: Added atomic check-and-serve requirement: rule and grant recheck in the same store read as the response and decrypt; mid-flight delete or revoke denies with 403.
- R2.3 (missing scenario): specs/output-sharing/spec.md: the stable-ID requirement covers only the producer. Renaming or removing a consumer unit or a consumer collection, which a rule names by stable ID, has no scenario, although ADR 273 decision 2 and the project rule require stable IDs for stored references. Resolution: answered: Extended stable-ID requirement with consumer rename still matches and consumer remove-and-make-again denies until a new rule names the new ID.
- R2.4 (open question): specs/output-sharing/spec.md: the secret-output scenario says values are sealed for the authorized path, but the change does not define the sealing or the decrypt path. The Checker needs the States port answer for an encrypted or unencrypted latest state, and that case is not covered. Resolution: answered: Added secret sealing requirement: secret values return as producer ciphertext, decrypt only through the decrypt-only path, with States client-encrypted bit scenarios.
- R2.5 (missing scenario): specs/output-sharing/spec.md: no scenario covers a read when the producer has no state yet, the latest state is missing, or the producer never checkpointed. Resolution: answered: Added no-latest-state scenario returning 404 with unit.not-found.
- R2.6 (missing scenario): specs/output-sharing/spec.md: the proposal promises error code unit.forbidden and a 403 naming unit:read-secrets, but the spec scenarios for denials name neither the code nor the action. Resolution: answered: Denial scenarios now state 403 with error code unit.forbidden and name unit:read-secrets for the secret path.
- R2.7 (missing scenario): specs/output-sharing/spec.md: the output routes requirement says only e2e-driven CLI versions are served, but gives no response for an undriven version or a version that cannot read the route. Resolution: answered: Added undriven-version scenario returning the version error that names supported e2e-driven versions.
- R2.8 (missing scenario): specs/output-sharing/spec.md: no scenario covers a job whose consumer unit is the producer itself, or a rule that names a collection with no units. Resolution: answered: Added self-read needs no rule and empty-collection rule matches no job scenarios.
- R2.9 (missing scenario): specs/output-sharing/spec.md: the rule-manage requirement names no route, no action for list (unit:read-access per ADR 273 decision 10) and no action for delete, so the authorization of each rule route is not stated. Resolution: answered: Rule-manage requirement now names PUT with unit:manage-sharing, GET list with unit:read-access, and DELETE with unit:manage-sharing on the same producer object.
- R2.10 (scope): tasks.md: task 2.2 gives an acceptance check only for the tool-neutral outputs route, with no check for the TFE-compatible routes or the terraform_remote_state path, although the spec covers all three. Resolution: answered: Build task check now requires tool-neutral, TFE-compatible, and terraform_remote_state paths each with 200 with rule and 403 without.
- R2.11 (open question): specs/output-sharing/spec.md: the hostile-kind scenarios accept either 400 or 403, and the denial audit is not stated for them. The response code and the audit entry are left open. Resolution: answered: Hostile-kind requirement now fixes 400 for unknown kind with validation error and 403 for principal as consumer, each with a denial audit entry.
- M.10 (consistency): Rule management duplicates add-output-sharing-setting-new. This change says only 'starter grants', but the other changes say unit:read-outputs. Resolution: answered: Removed the rule-management requirement from this change; this change now specifies only the cross-tool read check as a naming rule plus unit:read-outputs plus unit:read-secrets, and depends on add-output-sharing-setting-new for rule storage and on job-service-unit-settings-pool for job context.

## Open questions

none

Questions considered and answered:

- Who decides that a job of another unit reads outputs? ADR 273 decision 2 answers it: the producer decides alone, a rule gives a job of a consumer unit the right to read and gives a person nothing.
- What checks govern a cross-tool read of outputs? ADR 273 decisions 1 and 5 answer it: a naming rule plus unit:read-outputs of the starter at each read, plus unit:read-secrets for a secret output.
- Which routes serve cross-tool output reads? ADR 273 decisions 7 and 8 answer it: the tool-neutral outputs route, the named TFE-compatible output routes, and the Pulumi read of OpenTofu outputs through terraform_remote_state for e2e-driven versions.
- Where does sharing-rule management live? Rule storage, validation, list and delete live in add-output-sharing-setting-new; this change only reads stored rules by stable ID at check time.
- What happens when the audit write fails during a read or denial? Fail closed per project security rule. If the audit write fails the read fails with 500 and no outputs leave the server.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.

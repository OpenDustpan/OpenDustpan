# 273. Output sharing: the read-outputs action and sharing rules on the producer

Status: accepted · 2026-10-22. Records decisions 15.1 to 15.7 of the owner and the product manager. Accepts RFC 15. Supersedes in part ADR 213 decision 1, ADR 207 decision 7 and ADR 267 decision 14 (the action of check 2). Supersedes in part ADR 216 decision 8 and ADR 220 decision 12, for sharing rules only. Answers the open item of ADR 213 on a rule that names a unit of another namespace

## Context

[RFC 15](../rfc/0015-output-sharing.md) asks how one unit reads the outputs
of another unit. A Pulumi stack reference and an OpenTofu
`terraform_remote_state` data source both read another unit through the
backend. Today such a read needs `unit:read`, which also reads the whole state.

[ADR 213](0213-job-tokens-no-change-plans-bulk-calls-forge-connections-and-locks.md)
decision 1 already decides the two paths of a job: a job reads the outputs of
another unit only when a sharing rule names the unit of the job, and
the principal that started the job can read the source. It leaves open the
shape of the rule, the action, the default of a new unit, and a rule across
namespaces. [ADR 51](0051-collections-of-units.md) decision 8 lets a rule name
a namespace, a collection or a unit. [ADR 216](0216-namespace-administration-and-operator-scope.md)
decision 8 and [ADR 220](0220-isolation-between-namespaces-on-shared-infrastructure.md)
decision 12 make a rule across namespaces wait for the acceptance of both
namespaces.

The owner and the product manager decided the questions of RFC 15 on
22 October 2026 (15.1 to 15.7). OpenDustpan has no users, so no rule and no
grant needs a migration path.

Before this ADR, an e2e run checked what each CLI calls
today when it reads another unit. The table below gives the result. The new
e2e test `TestOutputReadPaths` keeps this check, and it fails when a CLI
version starts to call another route.

| Read | CLI versions | Routes of the producer | What the answer holds |
| --- | --- | --- | --- |
| Pulumi stack reference | 3.200.0 | `GET` stack, `GET .../export`, `POST .../decrypt` | The whole deployment |
| Pulumi stack reference | 3.250.0, 3.271.0 | `GET` stack, `GET .../export`, `POST .../batch-decrypt` | The whole deployment |
| OpenTofu `terraform_remote_state` | 1.11.14, 1.12.7, 1.13.2 | `GET` workspace by name, `GET .../current-state-version`, `GET /api/v2/state-versions/{id}/download` | The whole state |
| Pulumi `terraform` package 6.0.2, remote state reference | Pulumi 3.271.0, on a workspace of OpenTofu 1.11.14, 1.12.7 and 1.13.2 | The same three routes as `terraform_remote_state` | The whole state |
| `tfe_outputs` of the `tfe` provider 0.82.0 | OpenTofu 1.11.14, 1.12.7, 1.13.2 | None: the provider stops at service discovery | Not served |

The `tfe` provider needs the service `tfe.v2.2` in service discovery. The
server advertises `tfe.v2` and `tfe.v2.1` only. With `tfe.v2.2` added for the
check, the provider read the workspace by name with `include=outputs` and then
stopped with an error: the answer has no outputs relationship.

So no CLI version reads outputs only. Each read path reads a whole state.

## Decision

### Decisions of the owner and the product manager

1. **A separate action reads outputs (15.1, option a).** The new tool-neutral
   action `unit:read-outputs` allows a read of the root outputs of the latest
   state of a unit, and nothing else. Its object is a unit, a collection or a
   namespace. A collection and a namespace widen the object (ADR 51 decision
   8). A secret output also needs `unit:read-secrets`, as in ADR 213 decision 1.
   `unit:read` does not imply `unit:read-outputs`: the check reads grants of
   `unit:read-outputs` only. So a starter of a job with a custom role that
   holds `unit:read` and not `unit:read-outputs` fails check 2 of decision 5.
   Each built-in role that holds `unit:read` also holds `unit:read-outputs`,
   so no holder of a built-in role loses a read.

   Benchmark: HCP Terraform has a state permission "read outputs only" apart
   from "read" of the state, and remote state sharing on each workspace.
2. **The producer holds the sharing rule.** The producer is the unit that
   makes the outputs. A rule is an object of the producer namespace. Its
   producer is a unit, a collection or a namespace. Its consumer is a unit, a
   collection or the namespace of the producer. The unit that makes the
   outputs decides who reads them. A rule gives a job of a consumer unit the
   right to read. A rule gives a person nothing. A person reads outputs only
   with a grant of `unit:read-outputs`.
3. **A rule across namespaces names units or a collection (15.4, option b).**
   A rule can name a unit or a collection of another namespace as its
   consumer. It never names a whole other namespace. It never names a
   principal of any namespace. The producer decides alone: the rule needs no
   acceptance by the other namespace. The other namespace only gains a read,
   and the namespace that gives its outputs is the one that agrees.
   - The write of the rule carries the fence of the producer namespace only.
     It changes nothing in the other namespace.
   - The save does not tell whether the unit or the collection of the other
     namespace exists, as ADR 220 decision 11 requires. So the store keeps the
     reference of such a consumer, and matches it at each read. A rename in
     the other namespace ends the match. A new unit or a new collection with
     the old name matches. The docs page says so.
   - A job of the other namespace also needs check 2 of decision 5: the
     principal that started the job holds `unit:read-outputs` on the producer.
     So the producer namespace gives that grant too.

   - A denial of a read by a job of another namespace answers as a unit that
     does not exist: 404, with the same body as for a producer that does not
     exist (ADR 220 decision 11). This applies to each check: no rule, no
     grant of the starter, no grant for a secret output, and a job or a
     starter that is not known. So a job cannot probe for the units of
     another namespace. A denial of a read by a job of the producer
     namespace answers 403, as before.
   - The audit entry of such a denial goes to the audit log of the producer
     namespace only. It names the check, the producer and the unit of the
     job by its reference. The namespace of the job gets no entry, and no
     answer to the job names the check or the producer.
   - In the port, such a denial unwraps to `outputshare.ErrUnknownProducer`
     only. It never unwraps to `identity.ErrDenied`, also when its cause is
     an error of `identity.Authorizer` that wraps `identity.ErrDenied`. The
     cause stays in a field of the error for the audit entry. So the order in
     which an adapter tests the errors cannot turn the 404 into a 403.
   - **An unknown producer takes the same work as a hidden denial.** The
     answer is not the only signal: a job can repeat a request many times
     and measure the time. So a tool adapter calls the `Checker` also when
     the route names a producer that does not exist, with the reference of
     the route and no stable ID. The `Checker` runs each check and each port
     call of the read before it decides, also after a check fails and also
     for an unknown producer: it reads the job and the principal whose
     grants decide, runs `Store.Find`, the grant lookups and the read of the
     latest state, and then writes one audit entry of the same kind in the
     namespace of the route, when that namespace exists. So an unknown
     producer and a hidden denial run the same code and write the same audit
     entry, and the work does not depend on which check fails. This also
     records each probe in the audit log of the namespace that it probes.
     The suite proves the same port calls and the same audit entry. It does
     not measure time.

   This decision supersedes ADR 216 decision 8 and ADR 220 decision 12 for
   sharing rules. For each point of ADR 216 decision 8:
   - "The join is pending until an administrator of the other namespace
     accepts it": superseded. A rule is active when the producer saves it.
   - "An administrator of either namespace can end the join alone":
     superseded. Only a holder of `unit:manage-sharing` on the producer, its
     collection or its namespace removes a rule. The consumer namespace
     cannot end a rule that names one of its units.
   - "The operator cannot make a join alone, also not with a grant on
     `@server`": this stays. A grant on `@server` does not give
     `unit:manage-sharing` for a rule whose consumer is in another
     namespace. The save of such a rule needs the action from a grant in the
     producer namespace: on the producer, its collection or its namespace.
   - "Each step writes an audit entry in the scope of each namespace":
     superseded. The save and the removal of a rule write one audit entry in
     the producer namespace only. The consumer namespace gets no entry. An
     entry there would need a write in the other namespace, with its fence,
     and would tell whether the consumer exists.

   A namespace link and a dependency edge across namespaces still need the
   acceptance of both namespaces, as ADR 216 decision 8 says.
4. **A namespace setting decides the rule of a new unit, and the default is
   "nobody" (15.6, option c).** The namespace setting
   `outputSharing.newUnitDefault` has the values `nobody` and `namespace`. The
   default is `nobody`.
   - With `nobody`, a new unit gets no rule. No job of another unit reads its
     outputs until a person saves a rule.
   - With `namespace`, the store writes one rule on a new unit that names the
     namespace of the unit. The SQL adapter of the store writes the unit, the
     rule and the audit entry of the rule in the one transaction that makes
     the unit. The three commit together, or none of them commits. This
     write is inside the SQL adapters. It is not a port of the public SDK
     (ADR 272), and the contract of `outputshare.Store` states it.
   - A change of the setting changes no rule of a unit that exists.
   - The escape hatch is a rule: on one unit, or one rule on a collection or
     on the namespace that shares the outputs of all its units (ADR 213
     decision 1).

   Benchmark: HCP Terraform shares the state of a new workspace with no other
   workspace unless the organization changes the default.
5. **A job token reads only what a rule names and the starter can read
   (15.5, option c).** A job token reads the outputs of another unit only when
   both checks pass at each read:
   - Check 1: a sharing rule of the producer names the unit of the job, its
     collection or its namespace.
   - Check 2: the principal that started the job holds `unit:read-outputs` on
     the producer with its own current grants, and `unit:read-secrets` for a
     secret output.

   The job gets the intersection of the two checks, never more. Check 2 uses
   `unit:read-outputs` in place of `unit:read`. This supersedes that part of
   ADR 213 decision 1.

   The principal whose grants decide check 2 is one of these:
   - The principal that started the job (ADR 213 decision 1).
   - The last saver of the trigger that started the job (ADR 267 decision
     14).
   - For an open of the profile block `outputs/opendustpan` by a job, the
     attachment authority (ADR 207 decision 7): the principal that made the
     attachment, or the blueprint publisher of a Catalog attachment. The
     grants of the starter do not count.

   No caller gives the `Checker` a principal whose grants decide. For an
   open of the profile block, the profile service passes only a reference
   (`ReadRequest.Attachment`): the ID of the attachment, and the profile,
   the revision and the block that the job reached through it. The
   reference names the attachment, because the authority belongs to the
   attachment (ADR 203 decision 4). One profile can have more than one
   attachment that covers the unit of the job, for example one on its
   collection and one on the unit, each with its own authority. When the job
   reaches a block of one name through more than one attachment, the
   reference names the attachment whose block the merge keeps: the later
   one (ADR 203 decision 5).

   The `Checker` does not trust the reference. It reads the attachment
   through the small port `outputshare.Attachments`, which the profile
   store serves, for the job of the job-ID claim of the job token. The port
   answers from the spec of that job, which fixed the attachments and their
   revisions at the start of the job (ADR 203 decision 5). It answers only
   when the spec holds that attachment and, through it, that revision of
   that profile, as the attached profile or as a profile that it imports or
   reads, that revision holds the block, and the merge keeps the block from
   that attachment. The port returns the ID of the
   attachment, its object (a unit, a collection or a namespace), the
   producer that the block names and the authority. A block of an imported
   profile uses the authority of the attachment through which the job
   reached it.

   The `Checker` denies with the check `attachment` unless the port answers,
   the ID is the ID of the reference, the object covers the unit of the job
   (the same unit, its collection or its namespace, by stable ID), and the
   block names this producer by stable ID. A unit that moved out of the
   collection of the attachment after the start of the job is not covered,
   so the read fails closed. So a job, an SDK caller or an adapter that
   copies a value by mistake cannot read with the grants of another
   principal. A reference to the attachment of an administrator on another
   unit, to an attachment or a revision that the spec of the job does not
   hold, to the spec of another job, or to a block that names another
   producer denies, and the denial goes to the audit log. Of two
   attachments of one profile that cover the unit, only the authority of
   the attachment whose block the merge keeps decides. A reference to the
   other attachment denies, also when its authority holds the grant.

   A direct open is a read by the principal with the grants of the caller,
   and the `Checker` refuses an attachment reference on a read by a
   principal.
   We chose a reference and a port over a type that only the profile
   package can make. Go cannot stop another package from making a value of
   an exported type of `pkg/outputshare`, and a type of `pkg/profile` in the
   port would make `pkg/outputshare` import the profile package.

   For each of them, check 2 uses `unit:read-outputs` in place of
   `unit:read`. Each other part of ADR 207 decision 7 and ADR 267 decision 14
   stays.

   Benchmark: AWS IAM gives a session the intersection of the session policy
   and the policy of its role.
6. **A decrypt for a caller that only reads outputs decrypts only outputs
   (15.2, option a).** For the Pulumi CLI, a secret output reaches the
   consumer as ciphertext of the producer, and the CLI asks the producer to
   decrypt it. A job token of another unit that passes both checks of
   decision 5 can decrypt only ciphertext that the root outputs of the latest
   checkpoint of the producer hold.
   - A principal decrypts only with `unit:decrypt`, as before.
     `unit:read-outputs` and `unit:read-secrets` give a principal no decrypt.
     No CLI path needs it: a principal with only those actions cannot get the
     ciphertext, because the export needs `unit:read` (decision 7).
   - The rule applies to `decrypt` and to `batch-decrypt`. The e2e run shows
     that 3.200.0 calls `decrypt`, and 3.250.0 and 3.271.0 call
     `batch-decrypt`.
   - A batch with one ciphertext that is not such an output fails as a whole
     with 403, and the server writes the denial to the audit log.
   - A config secret of the producer is not an output. It uses the same key,
     and its ciphertext is often in git. So this rule stops a consumer that
     decrypts the config secrets of the producer.
   - The server reads the latest checkpoint to find the outputs. ADR 41 lets
     the server read state, so this needs no exception.
   - The server reads the latest checkpoint at the decrypt. When the producer
     saves a new checkpoint between the export and the decrypt, a ciphertext
     that only the older checkpoint holds answers 403. This fails closed. The
     next run of the job reads the new checkpoint. The docs page states it.

   OpenTofu has no such case: a sensitive output is plain text in the state.
7. **The action covers only the paths that read outputs (15.3, option a).**
   `unit:read-outputs` covers only a route that answers outputs and nothing
   else:
   - the tool-neutral route
     `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/outputs`,
   - the TFE-compatible routes `current-state-version-outputs` and
     `state-version-outputs/{id}`,
   - the read of a workspace that `tfe_outputs` makes, when a slice serves it,
   - the decrypt of decision 6.

   A route that answers a whole state (the export of a stack, the download of
   a state version) still needs `unit:read` for a principal. So a stack
   reference and `terraform_remote_state` on a workstation need `unit:read`
   on the producer. The console states this limit on the sharing settings of
   a unit. For a job token of another unit, ADR 213 decision 1 stays: those
   routes answer the root outputs only, after the checks of decision 5.

   **The routes that a CLI calls with a job token of another unit.** Each
   route below declares `unit:read` for a principal, as before. For a job
   token of another unit, it declares `unit:read-outputs` on the producer
   and calls the `Checker` in place of the grant check, with
   `ReadRequest.Latest` set to the ID of the state that it answers (the
   state version, or the version of the checkpoint). The `Checker` reads the
   latest state of the producer through the port `outputshare.States`, and
   denies when the ID is empty or is not that of the latest state. So the
   caller does not declare that the read is of the latest state: the
   `Checker` compares it.
   It answers only the fields in the table. Each other field is left out or
   has its zero value: no stack tags, no VCS settings, no variables, no
   resource count, no lock holder.

   | Route | Answer to the job token |
   | --- | --- |
   | `GET /api/stacks/{namespace}/{project}/{stack}` | `orgName`, `projectName`, `stackName`, `id` and `version` of the latest checkpoint. `tags` is empty, `activeUpdate` is empty. |
   | `GET /api/stacks/.../export` | The reduced Pulumi deployment below. |
   | `POST /api/stacks/.../decrypt`, `POST /api/stacks/.../batch-decrypt` | Decision 6. |
   | `GET /api/v2/organizations/{organization}/workspaces/{workspace}` | `id`, `name` and the `current-state-version` relationship. `locked` is false and the lock fields are empty. `permissions` allows the read of state versions only. No other relationship. |
   | `GET /api/v2/workspaces/{id}/current-state-version` | `id`, `serial`, `lineage`, `terraform-version`, `status`, `hosted-state-download-url`, and `md5` and `size` of the reduced document. No upload URL, no `holder`, no resource relationship. |
   | `GET /api/v2/state-versions/{id}/download` | The reduced OpenTofu document below, for the current state version only. |
   | `GET /api/v2/workspaces/{id}/current-state-version-outputs` | The outputs, as for a principal. |

   A slice adds a field to this table only when its e2e step shows that a
   supported CLI version needs it, and the review of that slice accepts it.

   **The reduced documents.** A secret or sensitive output is left out when
   `Decision.Secrets` is false.
   - Pulumi: the export envelope keeps `version` (3, the version of the
     deployment) and `features` when the checkpoint has it. The deployment
     keeps the `manifest` and `secrets_providers`, so that the CLI can
     decrypt. Its `resources` hold only the root `pulumi:pulumi:Stack`
     resource, with its `urn`, `type`, `"custom": false` and `outputs`, and
     no `inputs`. The server writes `custom` and does not depend on the
     default of the CLI. The deployment has no other resource and no
     `pending_operations`.
   - OpenTofu: `version`, `terraform_version`, `serial`, `lineage`,
     `outputs`, and `resources: []`. No `check_results`.

   The e2e step of each slice reads the answer that the CLI gets and asserts
   that it holds no other resource. A CLI that succeeds proves only that the
   answer is compatible, not that it is reduced.

   The reduced documents have no proof from a CLI yet. The e2e steps "a job
   token with a rule" (Pulumi) and "a job token across namespaces with a
   rule" (OpenTofu) skip until the harness has a job token. Each reduced
   document is compatible only when the slice that removes the skip runs
   its step on each supported CLI version: 3.200.0, 3.250.0 and the latest
   Pulumi, and 1.11, 1.12 and the latest OpenTofu. If a CLI version needs
   a field that this ADR leaves out, that slice adds it under the rule of
   the route table above.

   **Client-encrypted OpenTofu state fails closed.** The server cannot read
   an envelope that the client encrypted (`meta`, `encrypted_data`,
   `encryption_version`, ADR 255), so it cannot reduce it to the root
   outputs. An answer of the envelope as it is would give the whole state
   to a consumer that has the key. So when the latest state of the producer
   is client-encrypted, the `Checker` refuses each read of outputs only,
   with the check `client-encrypted`, after all other checks:
   - A job of another namespace gets the hidden 404, the same as for a
     producer that does not exist (decision 3).
   - A job of the producer namespace, and a principal that holds
     `unit:read-outputs` and not `unit:read` on the producer, get 409, with
     a message that names the reason: the client encrypted the state of the
     producer. This is the answer of the reveal of ADR 255.
   - A principal that holds `unit:read` on the producer is not an
     outputs-only caller. It reads the state and the outputs as before: the
     download answers the envelope, and the outputs routes answer what they
     answer today. The `Checker` reads that grant itself.
   - Each refusal writes an audit entry in the producer namespace, and none
     in the namespace of the job.

   The refusal covers `download`, `current-state-version`, the outputs
   routes and an open of the profile block. So the row of
   `current-state-version` needs no `md5` or `size` for such a producer.
   The port `outputshare.States` tells the `Checker` that the client
   encrypted the state (`State.ClientEncrypted`). A tool whose client never
   encrypts the state answers false. The word is not "sealed", which the
   glossary keeps for the encryption by the server.

   **The latest state only.** Decision 1 reads the outputs of the latest
   state. An outputs-only caller is a job token of another unit, or a
   principal that holds `unit:read-outputs` and not `unit:read` on the
   producer. For such a caller:
   - `state-versions/{id}/download` refuses an `{id}` that is not the current
     state version of the producer.
   - `state-version-outputs/{id}` refuses an output of a state version that
     is not current.
   - `export/{version}`, `updates`, `update/{update}` and the other history
     routes of a stack refuse the caller. `state-versions`,
     `state-versions/{id}`, `state-versions/{id}/resources` and
     `state-at/{at}` of a workspace refuse the caller too.

   Old outputs can hold values that were rotated. A refusal answers 403 to a
   caller of the producer namespace, and 404 to a job of another namespace,
   as decision 3 says. Each refusal writes an audit entry in the producer
   namespace. A principal that holds `unit:read` reads each version, as
   before.
8. **OpenDustpan supports a Pulumi read of OpenTofu outputs where e2e passes
   (15.7, option b).** The `terraform` package of Pulumi reads an OpenTofu workspace
   through the routes of `terraform_remote_state`. The same checks apply. The
   e2e run passes for Pulumi 3.271.0 with the package 6.0.2. OpenDustpan
   supports only the versions that an e2e step drives. OpenTofu has no built-in way to read
   the outputs of a Pulumi stack. Typed output contracts (a versioned schema
   of outputs) get their own RFC later.
9. **Decisions 6, 7 and 8 are policy now.** The e2e run of this ADR checks
   the routes of each CLI version in the table above. Each slice that serves a
   route extends the e2e step of that route. A CLI version that the e2e suite
   does not drive is not supported.

### The shape of the API and of the ports

10. **The routes (ADR 17).** Each route declares its action and its object.
    The prefix is `/api/dustpan/v1`.

    | Method and path | Action |
    | --- | --- |
    | `GET /namespaces/{namespace}/output-sharing` | `namespace:read` on the namespace |
    | `PUT /namespaces/{namespace}/output-sharing` | `unit:manage-sharing` on the namespace |
    | `GET /namespaces/{namespace}/units/{unit_id}/sharing-rules` | `unit:read-access` on the unit |
    | `POST /namespaces/{namespace}/units/{unit_id}/sharing-rules` | `unit:manage-sharing` on the unit |
    | `DELETE /namespaces/{namespace}/units/{unit_id}/sharing-rules/{rule_id}` | `unit:manage-sharing` on the unit |
    | `GET /namespaces/{namespace}/units/{unit_id}/outputs` | `unit:read-outputs` on the unit |

    The routes of a rule on a collection and on a namespace have the same
    shape under `collections/{collection}` and under the namespace. The new
    action `unit:manage-sharing` saves and removes rules and changes the
    setting. Of the built-in roles, only `admin` holds it. A namespace can
    give it to others with a custom role.
11. **The ports.** The package `pkg/outputshare` holds the tool-neutral ports:
    `Store` (rules and the setting), `Checker` (each read), and the two
    small ports that the `Checker` reads: `Attachments` (the attachment
    of an open of the profile block, from the spec of the job, which the
    profile store serves) and `States` (the ID of the latest state of a unit, and whether
    the client encrypted it, which the store of each tool serves). The rule of a
    new unit is a write inside the SQL adapters (decision 4), not a port. `pkg/outputshare/outputsharetest` holds their
    conformance suite. The service goes to `internal/outputsharesvc` (ADR
    272). The port of decision 6 names ciphertext, which only the Pulumi CLI
    has, so it is `stack.OutputCiphertexts` in `pkg/stack`, with its suite in
    `pkg/stack/stacktest`.
12. **Defense in depth (ADR 250).** Each layer stops a bad read alone:
    - The HTTP adapter checks the request with an allowlist of object kinds.
    - The store runs `Rule.Validate` at each write. It checks that each unit
      and each collection of the producer namespace that a rule names exists
      with its stable ID. It binds `Get` and `Delete` to the producer of the
      route, so that `unit:manage-sharing` on one unit does not reach a rule
      of another unit or of the namespace.
    - The checker runs `Rule.Validate` on each rule that it reads, and checks
      both conditions at each read, with the current grants. It reads the
      latest state itself and denies a read of another state, and of a
      state that the client encrypted. It reads the attachment authority
      itself from the spec of the job, and denies an attachment that the
      spec does not hold, that does not cover the unit of the job, or whose
      block names another producer.
    - A hidden denial unwraps to one sentinel only, so an adapter cannot
      answer it with 403.
    - The answer to a job token holds the root outputs only.
    - The decrypt route checks each ciphertext against the outputs.
    - An unknown value, a missing starter or an error of a port denies, and
      the denial goes to the audit log.

## Not decided here

- The fields of the workspace answer that `tfe_outputs` needs, and the
  service `tfe.v2.2` in service discovery. The slice that serves
  `tfe_outputs` decides them with its e2e step.
- How a Pulumi program in a job passes the job token to the `terraform`
  package. ADR 213 says that the job token never goes in an environment
  variable.
- Typed output contracts, in a new RFC.
- A refusal on the consumer side. A rule only gives a read: it cannot push
  data into the consumer, and a job reads another namespace only when its
  own code names the producer. So the model of decision 3, where the
  producer decides alone, does not hurt the consumer namespace. But that
  namespace cannot stop its jobs from reading other namespaces, and it
  cannot see which shares reach it. A later decision can add a setting of
  the consumer namespace, for example
  `outputSharing.crossNamespaceReads: allow | deny`. The `Checker` reads it
  at read time as one more condition that must pass, so it can only narrow
  a read. It writes nothing in the producer namespace and tells nothing
  about the producer. Its refusal writes the audit entry in the consumer
  namespace. It changes no stored rule, so it can come later. The product
  manager decided on 22 October 2026 not to add it now.

## Consequences

- This ADR accepts RFC 15. The pull request of this ADR lists its slices.
- The e2e suite gets `TestOutputReadPaths`, which runs now, and
  `TestOutputSharingRules`, which skips until the slices land. Each slice
  removes the skip of its own part.
- `pkg/outputshare` and `pkg/stack` get the ports and the conformance cases.
  Each case skips until the first adapter. No adapter, no SQL and no
  route lands with this ADR.
- The built-in roles get `unit:read-outputs` and `unit:manage-sharing` in the
  slice that adds the actions. The route test gets the new routes.
- With `identity.principalHome: namespace`, a principal of another namespace
  has no grant on the producer. So a job of another namespace passes check 2
  only when the producer namespace grants `unit:read-outputs` to a principal
  that it can see. The docs page says so.
- The glossary gets the terms **Sharing rule**, **Producer**, **Consumer**
  and **Output sharing default**.
- A feature that a user can see ships with its docs page (ADR 214). Each slice
  carries its page.

## Superseded decisions

| ADR | Decision | Superseded by | What changes |
| --- | --- | --- | --- |
| 213 | 1, check 2: `unit:read` on the source unit | Decision 5 | Check 2 needs `unit:read-outputs`. |
| 207 | 7, `unit:read` of the attachment authority | Decision 5 | The attachment authority needs `unit:read-outputs`. |
| 267 | 14, `unit:read` of the last saver | Decision 5 | The last saver needs `unit:read-outputs`. |
| 216 | 8, the acceptance of both namespaces, for a sharing rule | Decision 3 | The producer decides alone. Only the producer namespace ends a rule and gets the audit entry. The refusal of an operator with a grant on `@server` stays. A namespace link and a dependency edge keep the rule. |
| 220 | 12, the acceptance of both namespaces, for a sharing rule | Decision 3 | The producer decides alone. A namespace link and a dependency edge keep the rule. |

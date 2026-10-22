# 273. Output sharing: the read-outputs action and sharing rules on the producer

Status: accepted · 2026-10-22. Records decisions 15.1 to 15.7 of the owner and the product manager. Accepts RFC 15. Amends ADR 213 decision 1, ADR 207 decision 7 and ADR 267 decision 14 (the action of check 2). Supersedes in part ADR 216 decision 8 and ADR 220 decision 12, for output-sharing rules only. Answers the open item of ADR 213 on a rule that names a unit of another namespace

## Context

[RFC 15](../rfc/0015-output-sharing.md) asks how one unit reads the outputs
of another unit. A Pulumi stack reference and an OpenTofu
`terraform_remote_state` data source both read another unit through the
backend. Today such a read needs `unit:read`, which also reads the whole state.

[ADR 213](0213-job-tokens-no-change-plans-bulk-calls-forge-connections-and-locks.md)
decision 1 already decides the two paths of a job: a job reads the outputs of
another unit only when an output-sharing rule names the unit of the job, and
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
     the other namespace ends the match. A new unit with the old name matches.
     The docs page says so.
   - A job of the other namespace also needs check 2 of decision 5: the
     principal that started the job holds `unit:read-outputs` on the producer.
     So the producer namespace gives that grant too.

   This decision supersedes ADR 216 decision 8 and ADR 220 decision 12 for
   output-sharing rules. A namespace link and a dependency edge across
   namespaces still need the acceptance of both namespaces.
4. **A namespace setting decides the rule of a new unit, and the default is
   "nobody" (15.6, option c).** The namespace setting
   `outputSharing.newUnitDefault` has the values `nobody` and `namespace`. The
   default is `nobody`.
   - With `nobody`, a new unit gets no rule. No job of another unit reads its
     outputs until a person saves a rule.
   - With `namespace`, the store writes one rule on a new unit that names the
     namespace of the unit. It writes the rule in the transaction that makes
     the unit, with an audit entry.
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
   `unit:read-outputs` in place of `unit:read`. This amends ADR 213 decision 1.
   The same change applies to the attachment authority of the profile block
   `outputs/opendustpan` (ADR 207 decision 7) and to the last saver of a
   trigger (ADR 267 decision 14). Each part of those decisions stays.

   Benchmark: AWS IAM gives a session the intersection of the session policy
   and the policy of its role.
6. **A decrypt for a caller that only reads outputs decrypts only outputs
   (15.2, option a).** For the Pulumi CLI, a secret output reaches the
   consumer as ciphertext of the producer, and the CLI asks the producer to
   decrypt it. A caller that can only read outputs (a job token that passes
   both checks, or a principal with `unit:read-outputs` and
   `unit:read-secrets` and no `unit:decrypt`) can decrypt only ciphertext that
   the root outputs of the latest checkpoint of the producer hold.
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
    `Store` (rules and the setting), `NewUnitRules` (the rule of a new unit)
    and `Checker` (each read). `pkg/outputshare/outputsharetest` holds their
    conformance suite. The service goes to `internal/outputsharesvc` (ADR
    272). The port of decision 6 names ciphertext, which only the Pulumi CLI
    has, so it is `stack.OutputCiphertexts` in `pkg/stack`, with its suite in
    `pkg/stack/stacktest`.
12. **Defense in depth (ADR 250).** Each layer stops a bad read alone:
    - The HTTP adapter checks the request with an allowlist of object kinds.
    - The store runs `Rule.Validate` at each write.
    - The checker runs `Rule.Validate` on each rule that it reads, and checks
      both conditions at each read, with the current grants.
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
| 216 | 8, the acceptance of both namespaces, for an output-sharing rule | Decision 3 | The producer decides alone. A namespace link and a dependency edge keep the rule. |
| 220 | 12, the acceptance of both namespaces, for an output-sharing rule | Decision 3 | The producer decides alone. A namespace link and a dependency edge keep the rule. |

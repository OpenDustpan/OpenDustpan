# 203. Opening a profile: sessions, attachments, delivery, actions and audit

Status: accepted · 2026-10-18 · part of ADR 200 · decision 12 amended by ADR 210 decision 6. The replay record holds a keyed hash. A route that does not take the key answers 400. Decision 5 superseded in part by ADR 225. Decision 1 extended by ADR 241

Changed by:

- ADR 210 changes decision 12: a keyed hash in the replay record, refusal of
  an unsupported key.
- ADR 222 changes decision 5: the open rule becomes a gate.
- ADR 225 changes decision 5: a job with an open rule waits in the approval
  wait.
- ADR 241 extends decision 1: the consumer import.
- ADR 286 changes: the words catalog and promotion become self-service and
  failover.
- ADR 300 changes decision 5: a job of a unit with an open rule waits. It does not fail.

## Context

An **open** resolves a profile for one caller: it merges the imports, calls
the provider blocks that the caller needs, checks the types and the rules,
and answers the values with their secret marks. RFC 5 asked that opening a
profile is an action of its own, apart from a read of a unit, so that a
person can deploy a unit without seeing the secrets of its profile. RFC 5
also asked that the audit log records each open, and never a value.

Accepted ADRs already give parts of the path from a profile to a run:

- The server fixes the profile of a job at start (ADR 100 decision 5).
- The secrets of a profile reach a runner through the runner protocol, as
  files on a memory-backed volume, never in a variable (ADR 101 decision
  10, ADR 133 decision 12, ADR 134 decision 7). The plan phase gets the
  read-only credentials of the profile where it has them (ADR 101
  decision 10).
- The launcher masks each secret that it got in each log (ADR 134
  decision 8).
- A grant, a profile attachment and an output-sharing rule can name a
  namespace, a collection or a unit (ADR 51 decision 8).
- A blueprint names the profile that supplies the secrets of its units
  (ADR 112 decision 8).
- An agent never sees a decrypted secret unless the secret gate is open
  (ADR 124). Dusty observes a unit with short-lived, read-only credentials
  of the profile of the unit (ADR 122 decision 8).

ESC opens an environment into a session that lives 2 hours by default,
and the Pulumi CLI reads the values in a second request. ESC sends events
of its environments to webhooks.

## Decision

1. **The open request.** `profile.Service.Open` takes an actor, the
   profile with an optional `@tag`, `@number` or `@draft:<ID>`, the paths
   to answer (all paths by default), the consumer and the duration of the
   session. The consumer is one of `api`, `console`, `run` (the run
   command), `pulumi`, `opentofu`, `launcher` and `observe`. The server
   builds the context of ADR 205 from its own records. A caller never
   gives a claim.
2. **The resolve.**
   1. Read the revision of each profile that the open reaches, with its
      imports, once each.
   2. Find the provider blocks that the asked paths and the exports of the
      consumer need. Order them so that each login block comes before each
      block that names it.
   3. Call the blocks, at most 16 at the same time, each one once. A failure
      of a block fails the open with `profile.ErrProvider`, which names the
      block, the profile and the error class of ADR 206 decision 7. An open
      never answers a part of the values as if it were all of them.
   4. Merge, check the types and run the `checks` (ADR 201).
   5. Answer the values and the exports, each leaf with its marks. The
      expiry of the result is the earliest expiry of a credential in it,
      or the end of the duration.
3. **Open sessions.** An open with the consumer `api`, `console` or
   `pulumi` stores its result as an **open session** with an ID of the
   prefix `pos-`. A read of the session answers the values, or one path.
   - The duration is 1 minute to 12 hours, 2 hours by default. A session
     ends at the end of its duration or at the first expiry of a
     credential in it, whichever is first.
   - The store seals the result with the data key of the root profile. A
     schedule deletes each session that ended. A principal has at most 100
     sessions of direct opens that did not end. A job has at most 100
     sessions that did not end, so a principal of a CI system with many
     jobs at the same time does not reach the limit.
   - Only the credential that opened a session reads it: the same
     principal, and for a job token the same job. A person who started a job
     cannot read a session of the job with a personal token.
   - Each read of a session checks the action again, as at the open: for a
     direct open, `profile:open` on each profile of the session, and an
     access request that is still valid when an open rule applies. For a
     job open, it checks that the job is not terminal, and the attachment
     authority as at the open (decision 4). A read that fails the check
     fails with `identity.ErrDenied`, and the server deletes the session.
     So a revoked grant or an expired access request ends the session at
     the next read, not at the end of its duration.
   - Sessions are not in a backup, because they hold credentials that end
     within hours.
   - The other consumers get the result in the answer and the server
     stores nothing.
4. **Who can open.**
   - **A job open** is each open with a job token, whatever the consumer.
     A direct open is each open with any other credential.
   - **A direct open** (for example the consumers `api`, `console`, `run`,
     and `pulumi` or `opentofu` on a workstation) needs
     `profile:open` on the root profile and on each profile that the open
     evaluates, as imports or with `profiles[...]`. The error names the
     first profile that the caller cannot open.
   - **A job open** (the consumer `launcher`, and `pulumi`, `opentofu` or
     any other consumer with a job token) opens only the profiles that the
     spec of the job
     fixed, at the revisions that the spec fixed (decision 5). The
     attachments decide, not the actions of the person who started the job.
     So a person who can run a plan of a unit does not need
     `profile:open`.
   - **The job is not a hard boundary.** The code of the unit runs in the
     job and can read each file and variable that the job gets. So each
     person who can push code that a job of an attached unit runs, or make
     a unit in an attached collection, can get the credentials of the
     profile at the access of that job. The real boundary is the trust
     policy at the cloud or the remote: it names the `access`, the
     `ref_protected` and the `unit_id` claims of ADR 205 that it accepts.
     The security docs say so, with an example policy for each cloud.
   - **The attachment authority.** Each check that an open makes for a
     principal, for example `unit:read-secrets` on the source unit of
     `outputs/opendustpan` (ADR 207), uses the caller for a direct open.
     For a job open, it uses the **attachment authority**: the principal
     that made the attachment, or for a Catalog attachment the publisher
     of the blueprint. The attachment stores that principal.
   - **The server checks the authority again at each open of a job**: at
     the job start, at each open of a step (decision 6), at each token
     renewal (decision 7) and at each read of a session of the job
     (decision 3). It checks the full set of the attach checks, and then
     the reads of each block. Each failure fails the open with
     `profile.ErrAttachmentAuthority`, which wraps `identity.ErrDenied`
     and names the attachment, the authority and the action:

     | Action of the authority | Object | When it is missing |
     | --- | --- | --- |
     | the principal is active | | The principal is disabled, deleted or left the namespace. The error says `authority disabled`. |
     | `profile:open` | the attached profile, and each profile that it imports or reads | The error names the first profile. |
     | `profile:attach` | the attached profile | |
     | `unit:manage` | the object of the attachment (a namespace, a collection or a unit) | |
     | `unit:read`, and `unit:read-secrets` for a secret output | the source unit of `outputs/opendustpan` (ADR 207 decision 7) | The error also names the block. |

     So a revoked grant fails the next open of a job, also in a job that
     runs, and not only the next job.
   - **Re-authorize.** When an authority is no longer valid, each job of
     each unit of the attachment fails. `POST
     .../profile-attachments/{attachment}/reauthorize` makes the caller the
     attachment authority. The caller must pass the attach checks of
     decision 5 at that time. The route writes an audit entry with the
     authority before and after. It needs `If-Match`, so two people do not
     replace each other.
   - **The access review.** `GET .../profile-attachments?authority=invalid`
     lists each attachment of the namespace whose authority fails an
     attach check now, with the missing action. The access-review export of
     ADR 52 holds the same list. A schedule checks the authorities each day
     and sends `profile.attachment-authority-invalid` for each attachment
     that became invalid, so a team finds it before a job fails.
   - **Shared outputs.** An open of `outputs/opendustpan` by a job needs
     both: an output-sharing rule of the source unit names the unit of the
     job, and the attachment authority holds `unit:read`, and
     `unit:read-secrets` for a secret output, on the source unit (ADR 207
     decision 7). The result is the intersection of the two. ADR 213
     decision 1 (in review) gives the same intersection for a read of the
     job token, with the principal that started the job in place of the
     attachment authority. The two reads are different: a job token reads
     outputs through the API with the rule of ADR 213, and an open of a
     profile reads them with the rule of this ADR. ADR 200 decision 9 lists
     this for ADR 213.
   - **An agent** opens with masked values only: each secret answers
     `[masked]`, and no login block runs. No path gives an agent a value of
     a profile in plain text, also with the secret gate of ADR 124 open,
     because ADR 124 decision 1 allows only `read_state`. The observe of
     Dusty (ADR
     122 decision 8) runs login blocks inside the server with the consumer
     `observe` and the access `read`, and the credential never leaves the
     server.
   - A save that adds an import or a `profiles[...]` read needs
     `profile:open` on that profile for the author. So an author cannot
     pass a profile that the author cannot open to the people who can open
     the importer.
5. **Attachments decide what a job reads.** A **profile attachment** names
   a profile, an object (a namespace, a collection or a unit, ADR 51
   decision 8), a revision tag (`latest` by default) and a position.
   - The profiles of a unit are the attachments of its namespace, then of
     its collection, then of the unit itself, each list in order of
     position. They merge as imports do: a later one wins. So a unit can
     replace a value of its collection, but not a value that a wider
     attachment marks `$final` (ADR 201): such a change fails the job
     start with `profile.ErrFinal`. Every check of every profile that the
     job evaluates runs, from each attachment and each import. A narrower
     attachment cannot delete a check.
   - A blueprint names a profile in `platform.profile` (ADR 112 decision
     8). The Catalog makes a unit attachment with that profile when it makes
     the unit. It uses the authority of the publisher of the blueprint, not
     of the requester: the publish of a blueprint that names a profile
     needs `profile:open` and `profile:attach` on that profile, and the
     publish writes an audit entry that names the profile. A requester needs
     no `profile:open`. The attachment that the Catalog makes names the
     publisher as its attachment authority (decision 4), and its audit
     entry names the request, the blueprint and the publisher.
   - A new version of a blueprint can have another publisher. The publish
     of that version checks the new publisher as above. An existing
     attachment keeps the authority of the publisher of the version that
     made it. When a unit moves to the new version, its Catalog attachment
     gets the publisher of the new version as its authority, in the same
     transaction, with an audit entry. When the new version names another
     profile, the move replaces the attachment. Until a unit moves, an
     attachment whose earlier publisher is no longer valid is in the access
     review (decision 4), and a person can re-authorize it.
   - At the start of a job, the server resolves the attachments of the
     unit to fixed revisions and stores them in the spec, as ADR 100
     decision 5 says. It also fixes the revision of each profile that they
     import or read with `profiles[...]`, so each phase of the job reads the
     same set of revisions, also when a tag moves between two phases. A later change of an attachment or of a tag does not
     change the job. The job record names each profile and revision, so a
     reader of the job sees what the job read.
   - An attach and a detach need `profile:attach` on the profile and
     `unit:manage` on the object, which a collection or a namespace widens
     (ADR 51 decision 8). An attach and a re-authorize also need each check of the table of decision 4 for the caller. These are `profile:open` on the profile and on each profile that it imports or reads, and the reads of each `outputs/opendustpan` block. Each job of the object then reads their secrets. An
     attach writes an audit entry.
   - A job of a unit whose profiles have an open rule (ADR 202 decision 7)
     fails at start with `profile.ErrOpenRule`. RFC 5 keeps open how the
     gates of a job and the open rule work together.
6. **Delivery to a runner.** For each step of the role `tool`, the launcher
   asks for its credentials through its job session (ADR 133 decision 12).
   The server opens each profile of the spec with the consumer `launcher`
   and the context of the phase, and answers:
   - `exports.files`, and the default exports of each block with `export:
     true`. The launcher writes each one on its memory-backed volume and
     sets the variable to the path (ADR 134 decision 7).
   - `exports.variables`. The job start refused each secret in it (ADR 201
     decision 9), so each one holds no credential.
   - `exports.opentofu`, for an OpenTofu unit: the launcher writes the
     values as a variables file on its volume and adds
     `-var-file=<path>` to the tool step (`launcher.OpenTofuVarFile`).
     OpenTofu keeps the values of variables in a saved plan, so the stored
     plan holds them. A stored plan is sealed with its item key (ADR 151).
     A value marked never store fails the job start with
     `profile.ErrNeverStore`, which names the path and the ephemeral
     resource of ADR 209.
   - `exports.pulumi`, for a Pulumi unit: the launcher writes a stack
     settings file that is the file of the checkout plus the profiles of
     the spec in `environment`, each as `name@<revision>`, and adds
     `--config-file=<path>` to the tool step (`launcher.PulumiConfigFile`).
     The Pulumi CLI then opens them through the Pulumi Cloud-compatible
     API with the job token (ADR 209 decision 5).

   The plan phase opens with the access `read`, and the apply phase with
   `write`. A login block that gives a role for each access (ADR 201) then
   gives the read-only role to the plan phase, as ADR 101 decision 10 asks.
7. **Tokens in files get renewed.** A login block in `token` mode (ADR 207
   decision 2) gives a token that lives 15 minutes. The launcher asks for a
   new one through its session at two thirds of its life, and replaces the
   file with a rename, so a reader never sees half a file. A step that
   runs for hours keeps a valid token, and the SDK of the cloud reads the
   new file. The session path of the runner protocol is `POST /runner/v1/session/credentials/renew` (ADR 131), outside the versioned API (ADR 210, in review). It answers only the token files of the step. Each
   renewal checks the attachment authority again (decision 4).
8. **No cache across opens.** Each open calls its blocks again. A step of a
   job opens once. So each credential is as short-lived as its block says,
   and a revoke in a cloud applies at the next open. This rule does not
   cover open sessions (decision 3): a read of a session answers the
   values of its open, for at most the duration of the session.
9. **The audit entry of an open.** Each open writes one audit entry of the
   kind `access`, with the action `profile:open` and the consumer:
   - the root profile and each profile and revision that the open read,
   - the paths that it answered, never a value,
   - each block that it called, with its `Identity` (ADR 206) and the `jti`
     of each token,
   - the job, the phase and the unit when a job opens,
   - the outcome, and the error class of a failure.

   A refused open writes an entry too. A read of a session writes no entry,
   because the open wrote one.
10. **Notifications.** The catalog of types of ADR 145 decision 5 gets:

    | Type | When |
    | --- | --- |
    | `profile.created`, `profile.deleted`, `profile.restored` | The lifecycle of decision 9 of ADR 202. |
    | `profile.revision-created`, `profile.revision-retracted` | A save, a rollback, an approved draft, a retract. |
    | `profile.tag-changed`, `profile.label-changed` | A tag or a label changed. |
    | `profile.import-changed` | A profile that this profile imports got a new revision under the tag of the import. |
    | `profile.draft-submitted`, `profile.draft-approved` | The review of a draft (ADR 202 decision 6). |
    | `profile.access-requested`, `profile.access-approved` | The gate on opens (ADR 202 decision 7). |
    | `profile.rotation-succeeded`, `profile.rotation-failed` | A rotation (ADR 202 decision 8). `profile.rotation-failed` also comes each day for a record that stays `pending` or `orphaned`. |
    | `profile.attachment-authority-invalid` | The daily check found an attachment whose authority fails an attach check (decision 4). |

    A webhook sink of ADR 145 signs each delivery. A notification holds
    names, IDs, revisions and links, never a value (ADR 145 decision 4).
11. **Actions.**

    | Action | Object | Allows |
    | --- | --- | --- |
    | `profile:read` | the profile, or the namespace for a list | Read the metadata, the documents with sealed secrets, the revisions, the tags, the labels, the diff with no secret, the referrers and the claims preview. |
    | `profile:write` | the namespace for a create, else the profile | Create, save a revision or a draft, change tags and labels, roll back, clone, change a rotation schedule (a save). A routing change also needs `profile:open` (ADR 202 decision 2). |
    | `profile:open` | the profile | Open, read the static secrets of a document, a diff with secrets. |
    | `profile:approve` | the profile | Approve a draft or an access request (ADR 202). |
    | `profile:rotate` | the profile | Rotate now. |
    | `profile:attach` | the profile | Attach and detach, with `unit:manage` on the object, and `profile:open` for an attach or a re-authorize. |
    | `profile:manage` | the profile | Change the rules and the settings, rename, delete, restore, retract, deletion protection. |
    | `profile:redact` | the profile | Redact a revision (ADR 202 decision 2). |

    Built-in roles: each role with `namespace:read` gets `profile:read`.
    A role with `unit:write` gets no other profile action. Each other
    profile action goes only to the role that holds `namespace:admin`, and
    to custom roles through a grant (ADR 52), for example a grant of
    `profile:write` on one profile folder to a team. The reason is least
    privilege, designed against ISO/IEC 27001:2022 A.8.2 on privileged
    access rights: a person who deploys does not change, attach or detach the
    profiles that hold guardrails, for example the `checks` and `$final`
    values of a namespace attachment. So the default for a person who
    deploys is to deploy with a profile, and not to read or change it.
12. **Routes.** The routes are tool-neutral, under the prefix and the
    style of ADR 210 (in review). `{namespace}` is the namespace and
    `{profile}` is one path segment that holds the profile name or its
    `prf-` ID. The routes below drop the prefix
    `/api/dustpan/v1/namespaces/{namespace}`, except the two routes of the
    installation.
    - **A name of two segments is one escaped segment.** A client sends
      `payments/aws-prod` as `payments%2Faws-prod`. `ServeMux` matches each
      segment of the escaped path, so `%2F` stays in one wildcard, and
      `PathValue("profile")` answers `payments/aws-prod`. A route test
      checks this for each profile route: it sends a name with `%2F` and
      expects the profile, and it sends the same name with a plain `/` and
      expects `404`. With this rule, no route parses a path by hand, and no
      name segment needs a list of reserved words. A reverse proxy that
      decodes `%2F` before the server breaks these paths. The docs say
      so, and say that the `prf-` ID always works. A name never starts with
      `prf-`, so a segment is never both.
    - The objects of a namespace that are not one profile are collections
      of the namespace: `profile-labels`, `profile-sessions` and
      `profile-attachments`. The adapter specs and the context schema are
      objects of the installation: `/api/dustpan/v1/profile-adapters` and
      `/api/dustpan/v1/profile-context-schema`.
    - An update is a `PATCH` with JSON Merge Patch. Each answer that
      returns one resource holds its `ETag`. `PATCH`, `PUT`, `DELETE` and
      each write of a profile take `If-Match`, and a save requires it: the
      `ETag` of a profile names its newest revision, so a save on a stale
      read answers `412`. A list takes `pageSize` and `pageToken`. A time is
      an RFC 3339 string.
    - Each `POST`, `PATCH`, `PUT` and `DELETE` route below takes the `Idempotency-Key` header of ADR 210 (in review). The replay record holds the SHA-256 of the request body, never the body, because a save body holds `$secret` in plain text. The replay record of an open holds only the ID of its open session, never a value. An open with no session (the consumers `run`, `opentofu`, `launcher` and `observe`) ignores the key. Each call is a new open with its own audit entry (decision 8).
    - The operations that are not a create, a read, an update or a delete
      (`open`, `check`, `rollback`, `retract`, `redact`, `rotate`,
      `abandon`, `apply`, `clone`, `rename`, `restore`, `withdraw`,
      `reauthorize`, and `keys/rotate` and `keys/check-copy` of ADR 204)
      use a sub-path. ADR 210 has not decided the shape of a custom method.
      These routes change with that decision.

    | Method and path | Action |
    | --- | --- |
    | `GET`, `POST /profiles` | `profile:read`, `profile:write` |
    | `GET`, `DELETE /profiles/{profile}` | `profile:read`, `profile:manage` |
    | `GET`, `PATCH /profiles/{profile}/settings` | `profile:read`, `profile:manage`: deletion protection and the other settings of the profile |
    | `GET`, `POST /profiles/{profile}/revisions` | `profile:read`, `profile:write` for a save, with `If-Match` |
    | `GET /profiles/{profile}/revisions/{rev}` | `profile:read`, and `profile:open` with `showSecrets` |
    | `POST /profiles/{profile}/revisions/{rev}/retract` | `profile:manage` |
    | `POST /profiles/{profile}/revisions/{rev}/redact` | `profile:redact` |
    | `POST /profiles/{profile}/rollback` | `profile:write` |
    | `GET /profiles/{profile}/diff` | `profile:read`, and `profile:open` with `showSecrets` |
    | `GET /profiles/{profile}/tags` | `profile:read` |
    | `GET`, `PUT`, `DELETE /profiles/{profile}/tags/{tag}` | `profile:read`, `profile:write`. A `PUT` creates or moves the tag, whose whole body is the revision. |
    | `GET`, `PATCH /profiles/{profile}/labels` | `profile:read`, `profile:write` |
    | `GET /profile-labels` | `profile:read` on the namespace |
    | `POST /profiles/{profile}/open` | `profile:open`, or a job token (decision 4) |
    | `GET /profile-sessions/{session}` | the credential that opened the session (decision 3) |
    | `POST /profiles/{profile}/check` | `profile:read`: check a document with no save |
    | `GET`, `POST /profiles/{profile}/drafts` | `profile:read`, `profile:write` |
    | `GET`, `PATCH /profiles/{profile}/drafts/{draft}` | `profile:read`, the author |
    | `POST /profiles/{profile}/drafts/{draft}/reviews` | `profile:approve` |
    | `POST /profiles/{profile}/drafts/{draft}/apply` | the author or `profile:write`, and `profile:open` for a routing change (ADR 202 decision 6) |
    | `GET`, `POST /profiles/{profile}/access-requests` | `profile:read` (each request of the profile), `profile:open` |
    | `GET /profiles/{profile}/access-requests/{id}` | `profile:read` |
    | `POST /profiles/{profile}/access-requests/{id}/withdraw` | the requester |
    | `POST /profiles/{profile}/access-requests/{id}/reviews` | `profile:approve` |
    | `GET`, `PATCH /profiles/{profile}/rules` | `profile:read`, `profile:manage` |
    | `POST /profiles/{profile}/rotate` | `profile:rotate` |
    | `GET /profiles/{profile}/rotations` | `profile:read` |
    | `POST /profiles/{profile}/rotators/{block}/abandon` | `profile:rotate` |
    | `DELETE /profiles/{profile}/rotations/{record}` | `profile:rotate`, for an `orphaned` record only (ADR 202 decision 8) |
    | `POST /profiles/{profile}/clone`, `.../rename`, `.../restore` | `profile:write`, `profile:manage`, `profile:manage` |
    | `GET /profiles/{profile}/referrers` | `profile:read` |
    | `GET`, `POST /profile-attachments` | `profile:read`, and `profile:attach`, `profile:open` and `unit:manage`. A `GET` takes the filter `authority=invalid` (decision 4). |
    | `DELETE /profile-attachments/{attachment}` | `profile:attach` and `unit:manage` |
    | `POST /profile-attachments/{attachment}/reauthorize` | `profile:attach`, `profile:open` and `unit:manage`, with `If-Match` |
    | `GET /api/dustpan/v1/profile-adapters`, `GET /api/dustpan/v1/profile-context-schema` | any signed-in principal |

    The paths are proposals for the route table. The route test gets each
    one with its actions. The errors use the error shape of the
    tool-neutral APIs and the stable error codes of ADR 182, and each one
    names the object and the one action that helps.

## Consequences

- A person who deploys gets the credentials of a profile inside a job
  only, with the access of the phase.
- The runner protocol gets the renewal path of decision 7. The launcher
  gets `OpenTofuVarFile` and `PulumiConfigFile`. Both need a check with
  `make e2e` against each supported version of the tools.
- A saved OpenTofu plan holds the values of the profile variables. Sealed
  storage of plans keeps them safe at rest. A team that wants no secret in
  a plan uses the ephemeral resource of ADR 209.
- `pkg/profile` gets `Service`, the `Store` and `Sessions` ports and the
  `UnitOutputs` port, each with `profiletest` cases before an adapter.

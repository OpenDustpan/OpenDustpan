# 203. Opening a profile: sessions, attachments, delivery, actions and audit

Status: accepted · 2026-10-18 · part of ADR 200

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
     sessions that did not end.
   - Only the principal that opened a session reads it.
   - Sessions are not in a backup, because they hold credentials that end
     within hours.
   - The other consumers get the result in the answer and the server
     stores nothing.
4. **Who can open.**
   - **A direct open** (the consumers `api`, `console`, `run`, and
     `pulumi` or `opentofu` with a token that is not a job token) needs
     `profile:open` on the root profile and on each profile that the open
     evaluates, as imports or with `profiles[...]`. The error names the
     first profile that the caller cannot open.
   - **A job open** (the consumer `launcher`, and `pulumi` or `opentofu`
     with a job token) opens only the profiles that the spec of the job
     fixed, at the revisions that the spec fixed (decision 5). The
     attachments decide, not the actions of the person who started the job.
     So a person who can run a plan of a unit does not need
     `profile:open`, and cannot read the profile outside the job.
   - **An agent** opens with masked values only: each secret answers
     `[masked]`, and no login block runs. An open of a value in plain text
     goes through the secret gate of ADR 124. The observe of Dusty (ADR
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
     replace a value of its collection.
   - A blueprint names a profile in `platform.profile` (ADR 112 decision
     8). The Catalog makes a unit attachment with that profile when it makes
     the unit.
   - At the start of a job, the server resolves the attachments of the
     unit to fixed revisions and stores them in the spec, as ADR 100
     decision 5 says. A later change of an attachment or of a tag does not
     change the job. The job record names each profile and revision, so a
     reader of the job sees what the job read.
   - An attach and a detach need `profile:attach` on the profile and
     `unit:manage` on the object, which a collection or a namespace widens
     (ADR 51 decision 8). An attach writes an audit entry.
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
   new file. The session path is `POST /runner/v1/session/credentials/renew`.
   It answers only the token files of the step.
8. **No cache across opens.** Each open calls its blocks again. A step of a
   job opens once. So each credential is as short-lived as its block says,
   and a revoke in a cloud applies at the next open.
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
    | `profile.rotation-succeeded`, `profile.rotation-failed` | A rotation (ADR 202 decision 8). |

    A webhook sink of ADR 145 signs each delivery. A notification holds
    names, IDs, revisions and links, never a value (ADR 145 decision 4).
11. **Actions.**

    | Action | Object | Allows |
    | --- | --- | --- |
    | `profile:read` | the profile, or the namespace for a list | Read the metadata, the documents with sealed secrets, the revisions, the tags, the labels, the diff with no secret, the referrers and the claims preview. |
    | `profile:write` | the namespace for a create, else the profile | Create, save a revision or a draft, change tags and labels, roll back, clone. |
    | `profile:open` | the profile | Open, read the static secrets of a document, a diff with secrets. |
    | `profile:approve` | the profile | Approve a draft or an access request (ADR 202). |
    | `profile:rotate` | the profile | Rotate now, change a schedule. |
    | `profile:attach` | the profile | Attach and detach, with `unit:manage` on the object. |
    | `profile:manage` | the profile | Change the rules, rename, delete, restore, retract, deletion protection. |
    | `profile:redact` | the profile | Redact a revision (ADR 202 decision 2). |

    Built-in roles: each role with `namespace:read` gets `profile:read`.
    Each role with `unit:write` gets `profile:write` and `profile:attach`.
    `profile:open`, `profile:approve`, `profile:rotate`, `profile:manage` and
    `profile:redact` go only to the role that holds `namespace:admin`, and
    to custom roles through a grant (ADR 52). So the default for a person
    who deploys is to deploy with a profile and not to read its secrets.
12. **Routes.** The routes are tool-neutral, under `/api/profiles`. `{ns}` is
    the namespace and `{profile}` the profile name, with one or two
    segments.

    | Method and path | Action |
    | --- | --- |
    | `GET`, `POST /api/profiles/{ns}` | `profile:read`, `profile:write` |
    | `GET`, `DELETE /api/profiles/{ns}/{profile}` | `profile:read`, `profile:manage` |
    | `GET`, `POST /api/profiles/{ns}/{profile}/revisions` | `profile:read`, `profile:write` for a save, with `If-Match` on the revision |
    | `GET /api/profiles/{ns}/{profile}/revisions/{rev}` | `profile:read`, and `profile:open` with `showSecrets` |
    | `POST /api/profiles/{ns}/{profile}/revisions/{rev}/retract` | `profile:manage` |
    | `POST /api/profiles/{ns}/{profile}/rollback` | `profile:write` |
    | `GET /api/profiles/{ns}/{profile}/diff` | `profile:read`, and `profile:open` with `showSecrets` |
    | `GET`, `PUT`, `DELETE /api/profiles/{ns}/{profile}/tags/{tag}` | `profile:read`, `profile:write` |
    | `GET`, `PUT /api/profiles/{ns}/{profile}/labels` | `profile:read`, `profile:write` |
    | `GET /api/profiles/{ns}/labels` | `profile:read` on the namespace |
    | `POST /api/profiles/{ns}/{profile}/open` | `profile:open`, or a job token (decision 4) |
    | `GET /api/profiles/{ns}/sessions/{session}` | the principal of the session |
    | `POST /api/profiles/{ns}/{profile}/check` | `profile:read`: check a document with no save |
    | `GET`, `POST /api/profiles/{ns}/{profile}/drafts` | `profile:read`, `profile:write` |
    | `GET`, `PUT /api/profiles/{ns}/{profile}/drafts/{draft}` | `profile:read`, the author |
    | `POST /api/profiles/{ns}/{profile}/drafts/{draft}/reviews` | `profile:approve` |
    | `POST /api/profiles/{ns}/{profile}/access-requests` | `profile:open` |
    | `POST /api/profiles/{ns}/{profile}/access-requests/{id}/reviews` | `profile:approve` |
    | `PUT /api/profiles/{ns}/{profile}/rules` | `profile:manage` |
    | `POST /api/profiles/{ns}/{profile}/rotate` | `profile:rotate` |
    | `GET /api/profiles/{ns}/{profile}/rotations` | `profile:read` |
    | `POST /api/profiles/{ns}/{profile}/clone`, `.../rename`, `.../restore` | `profile:write`, `profile:manage`, `profile:manage` |
    | `GET /api/profiles/{ns}/{profile}/referrers` | `profile:read` |
    | `GET`, `POST`, `DELETE /api/profiles/{ns}/attachments` | `profile:read`, `profile:attach` and `unit:manage` |
    | `GET /api/profiles/adapters`, `GET /api/profiles/context-schema` | any signed-in principal |
    | `POST /api/profiles/{ns}/{profile}/revisions/{rev}/redact` | `profile:redact` |

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

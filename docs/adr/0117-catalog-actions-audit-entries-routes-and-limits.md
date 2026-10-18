# 117. Catalog actions, audit entries, routes and limits

Status: accepted · 2026-10-17. Route paths superseded in part by ADR 200. An open question answered, and the action of the options route superseded, by ADR 203

## Context

[ADR 17](0017-api-authorization-and-policy-for-every-feature.md) says that
every feature has a REST API, that every endpoint declares its actions and
the object of each action, and that every change passes a policy check.
ADR 103 decision 8 gives the actions of jobs. ADR 160 asks for a
`blueprint` object kind with `blueprint:read` and `blueprint:set-context`.
ADRs 110 to 116 decide the Catalog.

## Decision

1. **The objects.** A Catalog action applies to one of three objects: a
   namespace, a blueprint (ADR 112 decision 1), or the unit of a request. A
   request has no object of its own: a request and its unit have one owner,
   so the actions of a request apply to its unit. Before the unit exists,
   the object of a create is the blueprint.
2. **Actions for each operation.** `catalog.Requirements` maps an
   operation to its actions, and a table-driven test checks the map.

   | Action | Object | Allows |
   | --- | --- | --- |
   | `blueprint:read` | blueprint, or namespace for a list | Read a blueprint, its draft, its versions and its form. |
   | `blueprint:publish` | blueprint, or namespace for a new one | Write the draft, and publish it as a version. A publish that names a source principal also needs `principal:manage-tokens` on that principal (ADR 113 decision 10), and a publish with custom steps `unit:define-steps` on the blueprint. The publisher needs no grant on the sources. |
   | `blueprint:retire` | blueprint | Retire a version. |
   | `catalog:request` | blueprint | Submit a create, and, for a create, read the options, check a document with no submit, and render a document from git. |
   | `unit:edit-inputs` | unit | Submit an update of the request of the unit, also an upgrade of its blueprint version. |
   | `unit:request-destroy` | unit | Submit a revision with `ensure` set to `absent`. |
   | `catalog:read` | unit, or namespace for a list | Read the requests, their revisions, their checks and their decisions. |
   | `catalog:render` | unit, or blueprint for a create | Render a request document from git for the team's CI. A render also needs the action of its operation. |
   | `namespace:manage-policy` | namespace | Bind policy engines to kinds, and set the namespace policy documents and the settings of the adapters. |

   The rows of `unit:edit-inputs` and `unit:request-destroy` also cover
   the options, the check and the render of an update or a destroy. So
   `catalog.Requirements` maps the options, the check and the render by
   their operation, as the submit:

   | Route | Create | Update | Destroy |
   | --- | --- | --- | --- |
   | submit, check, options | `catalog:request` on the blueprint | `unit:edit-inputs` on the unit | `unit:request-destroy` on the unit |
   | render | `catalog:render` and `catalog:request` on the blueprint | `catalog:render` and `unit:edit-inputs` on the unit | `catalog:render` and `unit:request-destroy` on the unit |

   A render of a document equal to the last revision stores nothing and
   needs `catalog:render` only. So a token of the CI can never do more
   through a render than a submit of the same document can.

   The source principal of a blueprint needs `unit:read` on the collection
   of each source, or on the namespace for a source of the units of a
   blueprint (`catalog.SourceRequirements`). The server checks it for the
   principal at each computation of the options.

   The job actions of ADR 103 stay. A cancel of a revision needs
   `job:cancel` on the unit. An approval needs `job:approve` on the unit.
   Break-glass needs `namespace:break-freeze` on the namespace. The start of
   the job of a revision needs the Catalog action of the revision, not
   `unit:run-apply` or `unit:write-back` (ADR 110, "Changes requested in the
   job model", item 1). So a consumer of a blueprint can ask for a unit and
   cannot run the tool on it.
3. **Built-in roles.** Each built-in role with `unit:read` gets
   `catalog:read` and `blueprint:read`. A role that writes a unit gets
   `unit:edit-inputs`. `unit:request-destroy`, `catalog:request`,
   `blueprint:publish`, `blueprint:retire`, `catalog:render` and
   `namespace:manage-policy` are in no built-in role except the
   administrator of the namespace. A grant gives them.
4. **A list shows what the caller can read.** A list of requests returns only
   the requests whose unit the caller can read with `catalog:read`, and the
   creates whose blueprint the caller can request from. It says when it
   removed requests (ADR 17).
5. **Routes.** The routes are tool-neutral, under `/api/catalog`. The paths
   are proposals for the route table, and the route test gets each one with
   its action:
   - `GET /api/catalog/blueprints/{namespace}` lists the blueprints.
   - `GET`, `PUT /api/catalog/blueprints/{namespace}/{blueprint}/draft`.
   - `POST /api/catalog/blueprints/{namespace}/{blueprint}/versions`
     publishes the draft.
   - `GET /api/catalog/blueprints/{namespace}/{blueprint}/versions/{version}`.
   - `POST /api/catalog/blueprints/{namespace}/{blueprint}/versions/{version}/retire`.
   - `POST /api/catalog/options/{namespace}` (ADR 113 decision 11) takes
     `{"blueprint": {"name": ..., "version": ...}, "request": "<name>",
     "answers": {...}}`. `request` names the request of an update, and is
     empty for a create.
   - `POST /api/catalog/check/{namespace}` checks a document at `submit`
     and stores nothing. The server picks the operation from the document
     and the request, as a submit does.
   - `POST /api/catalog/requests/{namespace}` submits. The body is
     `{"document": <request.json>, "reason": "...", "breakGlass":
     {"reason": "..."}}`, and the header `Idempotency-Key` makes a retry
     safe. The answer is `201` with the revision and its job, `200` with
     the status `unchanged` (ADR 111 decision 6), `403` for a denial,
     `409` for `catalog.ErrInFlight`, `422` with every failure of the
     checks, and `503` for a check that cannot run.
   - `GET /api/catalog/requests/{namespace}` lists the requests.
   - `GET /api/catalog/requests/{namespace}/{blueprint}/{name}` and
     `.../revisions/{revision}`.
   - `POST /api/catalog/requests/{namespace}/{blueprint}/{name}/cancel`.
   - `POST /api/catalog/render/{namespace}` takes `{"connection": "...",
     "repository": "...", "commit": "...", "path": "..."}` and gives the
     files and the operation (ADR 110 decision 8). The server reads the
     request file at the commit from the forge, and the body holds no
     document. `connection` is optional, and `commit` is a full commit ID.
   - `GET`, `PUT /api/catalog/policy/{namespace}`.

   The errors use the error shape of the tool-neutral APIs. A failure of
   the checks is a list of `{input, code, message}`, so a form can show
   each failure next to its input. The context of a blueprint stays under
   `/api/context/blueprints` (ADR 160).
6. **Audit entries.** Each of these writes one audit entry in the
   transaction of the change:

   | Event | Subject | Action | Kind | Object |
   | --- | --- | --- | --- | --- |
   | Draft written | the principal, and the agent | `blueprint:publish` | `change` | blueprint |
   | Version published, with each source and the source principal | the principal | `blueprint:publish` | `change` | blueprint |
   | Version retired | the principal | `blueprint:retire` | `change` | blueprint |
   | Expr version recorded again (ADR 112 decision 11) | the instance | `blueprint:publish` | `change` | blueprint |
   | Submit accepted | the principal, and the agent | the action of the operation | `change`, or `override` with break-glass | blueprint for a create, else unit |
   | Submit refused by a check or a policy | the principal, and the agent | the action of the operation | `change`, outcome denied | as above |
   | Revision from git stored at a render, and the revision that it superseded | the principal of the render | `catalog:render` | `change` | blueprint for a create, else unit |
   | Recheck failed | the instance | the action of the revision | `change` | unit |
   | Policy settings changed | the principal | `namespace:manage-policy` | `change` | namespace |

   The entries hold the request ID and the revision as the version, the
   operation, the blueprint version, the combined effect of the policy, the
   policy versions, and the input names whose answers changed as the
   changes. They hold no answer value: an answer can name an internal
   system, and the revision holds it for the readers that `catalog:read`
   allows. The job of the revision writes its own entries (ADR 103
   decision 9). A read of options, of a check or of a render writes no
   entry.
7. **Limits of documents.** The server refuses a document above a limit
   with `catalog.ErrLimit`, which names the limit.

   | What | Limit |
   | --- | --- |
   | Request document | 64 KiB |
   | Answers in a request | 64 |
   | String answer | 4 KiB |
   | Items of a `string-list` answer | 100 |
   | Blueprint document | 256 KiB |
   | Inputs of a blueprint | 64 |
   | Validation rules of an input | 32 |
   | Policy rules of a blueprint | 64 |
   | Quota rules of a blueprint | 16 |
   | Expr source | 4 KiB |
   | Static values of a choice | 1,000 |
   | Filters of a source | 8 |
   | Chain of dependent choices | 8 |

8. **Limits of the store.**

   | What | Limit |
   | --- | --- |
   | Blueprints in a namespace | 500 |
   | Published versions of a blueprint | 1,000 |
   | Requests in a namespace | 10,000 |
   | Revisions in flight in a namespace | 1,000, the limit of open jobs (ADR 100 decision 16) |
   | Units that a source reads | 10,000 |
   | Options that the options route returns for an input | 1,000, then a truncated mark |
   | Submits for each principal | 60 in a minute, then `429` |

   The constants of `pkg/catalog` hold the numbers, and installation
   settings can change them later. A source above 10,000 units fails the
   check with `catalog.ErrLimit`, never with a partial list.

## Consequences

- A platform team gives a consumer `catalog:request` on one blueprint, and
  the consumer can ask for units of it and nothing else.
- An audit reader sees who asked for what, and the policy that decided. The
  values of the answers stay in the revisions.
- Nine actions join the action list of the `identity` package, with the
  object kind `blueprint`. The integrator adds them to the list, to the
  built-in roles and to the route test.

## Open questions

- Does a read of the options of a source need its own action, so that a
  platform team can let a person read a form and not submit it?
- Does a render need the job token of a CI job, or any access token with
  `catalog:render` and the action of the operation? Version 1 takes any
  token with the actions.
- Is 60 submits a minute for each principal the right number for a
  developer portal that submits for many people with one token?

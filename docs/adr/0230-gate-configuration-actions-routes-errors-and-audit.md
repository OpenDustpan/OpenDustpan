# 230. Gate configuration: actions, routes, errors and audit entries

Status: accepted · 2026-10-18. Part of ADR 222. Supersedes in part ADR 117 decisions 2, 5, 6 and 7. Supersedes in part ADR 196 decisions 1 and 5, and ADR 197 decisions 5 and 6

## Context

ADR 117 gives `namespace:manage-policy` and a route
`PUT /api/catalog/policy/{namespace}` for namespace policy documents. ADR
196 and ADR 197 give the change order errors `ErrPolicyDenied`,
`ErrPolicyFailed` and `ErrPolicyUnreadable`, with 403, 503 and 409. The
spike had `PUT` routes for gates, approval rules, approval defaults and
ownership rules, with 422 for a gate that is not valid and 409 for a final
gate.

## Decision

1. **In the server, through the API (owner).** Gates, access limits,
   approval rules, approval defaults, ownership rules, path sets and the
   signature carry-over setting are stored in the server. The console, the CLI and the IaC providers configure them through
   the routes of decision 3. No repository file holds them, and the server
   reads none.
2. **Actions.**

   | Action | Object | Allows |
   | --- | --- | --- |
   | `installation:manage-gates` | the installation | gates, access limits, approval rules, ownership rules, path sets and the signature carry-over setting of the installation |
   | `installation:read-gates` | the installation | read the gates, limits, approval rules, ownership rules and path sets of the installation |
   | `installation:break-gate-error` | the installation | the break-glass for a final gate of the installation in error (ADR 224 decision 13) |
   | `namespace:manage-gates` | namespace | gates, access limits, approval rules, approval defaults, ownership rules, path sets and the signature carry-over setting of the namespace, its collections and its units |
   | `namespace:break-gate-error` | namespace | the break-glass for gates in error of a change in the namespace, except a final gate of the installation |
   | `namespace:read-gates` | namespace | read them, and read the decision records |
   | `namespace:simulate-gates` | namespace | the simulate route |
   | `unit:edit-labels` | unit | change the labels of the unit, with no state write (ADR 223 decision 11) |

   `namespace:manage-gates` replaces `namespace:manage-policy`. It is in no
   built-in role except the namespace administrator. The two break-glass
   actions are in no built-in role. A change or a delete of a gate in
   `enforce` mode, of an ownership rule in `enforce` mode, of an access
   limit, of an approval rule, of a path set or of an approval default
   needs a fresh authentication (ADR 227). So does a change of a final gate
   in each mode, and a change of the signature carry-over setting to `on`.
   A reader of a decision record also needs the read action of the change
   that it explains.

   A gate of a blueprint version is part of the blueprint version and needs
   its Catalog action. A gate of a profile is part of the profile revision
   and meets `profile.save`. Neither has a route of decision 3.
3. **Routes (PM).** Under the prefix of ADR 210, the scope is part of the
   path. `{scope}` stands for each of these three prefixes:
   - `.../namespaces/{namespace}`
   - `.../namespaces/{namespace}/collections/{collection}`
   - `.../namespaces/{namespace}/units/{tool}/{name}`

   Each variable segment is percent-encoded (ADR 224 decision 1). So the
   Pulumi stack `web/prod` of `acme` is
   `.../namespaces/acme/units/pulumi/web%2Fprod`.

   | Route | Action |
   | --- | --- |
   | `GET {scope}/gates`, `GET`, `PUT`, `DELETE {scope}/gates/{id}` | read, manage |
   | `GET {scope}/access-limits`, `GET`, `PUT`, `DELETE {scope}/access-limits/{id}` | read, manage |
   | `GET {scope}/approval-rules`, `GET`, `PUT`, `DELETE {scope}/approval-rules/{id}` | read, manage |
   | `GET {scope}/ownership-rules`, `GET`, `PUT`, `DELETE {scope}/ownership-rules/{id}` | read, manage |
   | `GET {scope}/path-sets`, `GET`, `PUT`, `DELETE {scope}/path-sets/{id}` | read, manage |
   | `GET`, `PUT {scope}/approval-default` | read, manage |
   | `GET`, `PUT {scope}/signature-carry-over` | read, manage |
   | `POST {scope}/gates/check` | read |
   | `POST .../namespaces/{namespace}/gates/simulate` | simulate |
   | `GET .../namespaces/{namespace}/decision-records?change=<change ID>` | read |
   | `GET .../namespaces/{namespace}/decision-records/{id}` | read |
   | `POST .../namespaces/{namespace}/decision-records/{id}/break-glass` | `namespace:break-gate-error`, and `installation:break-gate-error` for a final gate of the installation |
   | `GET .../gates`, `GET .../gates/{id}`, and the same for `.../access-limits`, `.../approval-rules`, `.../ownership-rules` and `.../path-sets` | `installation:read-gates` |
   | `PUT`, `DELETE .../gates/{id}`, and the same for `.../access-limits/{id}`, `.../approval-rules/{id}`, `.../ownership-rules/{id}` and `.../path-sets/{id}` | `installation:manage-gates` |
   | `GET`, `PUT .../signature-carry-over` | `installation:read-gates`, `installation:manage-gates` |
   | `POST .../gates/check` | `installation:read-gates` |
   | `GET .../decision-records/{id}`, for a record of an access limit on an installation object | `installation:read-gates` |

   A list route gives the objects stored at that scope, not the objects of
   wider scopes. A `PUT` names no scope in its body, and refuses an unknown
   field. The `check` route type-checks a draft at the scope of its path
   and stores nothing. The installation `PUT .../signature-carry-over`
   takes `off` or `forbidden` only (ADR 225 decision 12).

   Benchmark: GitHub keeps repository rulesets and organization rulesets on
   separate paths (`/repos/{owner}/{repo}/rulesets` and
   `/orgs/{org}/rulesets`). The grant routes of OpenDustpan also name the
   scope in the path: `/api/server/grants`,
   `/api/server/namespaces/{namespace}/grants` and
   `/api/server/namespaces/{namespace}/collections/{collection}/grants`.
4. **Errors.** `pkg/gate` holds the sentinels, with these status codes and
   stable error codes (ADR 182):

   | Error | Status | Code |
   | --- | --- | --- |
   | `gate.ErrDenied` | 403 | `gate.denied` |
   | `gate.ErrLimited` | 403 | `gate.limited` |
   | `gate.ErrNotApprover` | 403 | `gate.not-approver` |
   | `gate.ErrAuthorApproval` | 403 | `gate.author-approval` |
   | `gate.ErrRuleFailed` | 409 | `gate.rule-failed` |
   | `gate.ErrFinal` | 409 | `gate.final` |
   | `gate.ErrInvalid` | 422 | `gate.invalid` |
   | `gate.ErrLimit` | 422 | `gate.limit` |
   | `gate.ErrUnavailable` | 503 | `gate.unavailable` |

   Each error names the decision record. `changeorder.ErrPolicyDenied`,
   `ErrPolicyFailed` and `ErrPolicyUnreadable` and `job.ErrPolicyDenied` wrap
   the `gate` errors. On a tool-compatible API, a refusal uses the error
   shape of that tool.
5. **Audit entries.** A create, a change and a delete of a gate, a limit or
   a rule write an audit entry of the kind `change`, with the old and the
   new version. A change that makes a final gate weaker also holds the
   reason (ADR 224 decision 12). A break-glass for gates in error writes an
   audit entry of the kind `override`, with the reason, the record ID and
   each gate that it passed. An evaluation writes no audit entry of its own. The audit
   entry of the step that it explains names the record ID. A switch of a
   gate between `enforce` and `evaluate` has its own entry.
6. **Limits.** A scope holds at most 256 gates, 64 access limits, 64 approval
   rules, 256 ownership rules and 64 path sets. A condition holds at most 4 KiB. Above a
   limit, the save fails with `gate.ErrLimit`.
7. **Backups.** A backup holds gates, access limits, approval rules,
   approval defaults, ownership rules, path sets, the signature carry-over
   settings and decision records (ADR 42).

Benchmark: AWS Verified Permissions and Google Cloud deny policies store
their policies in the service and give an API to manage them. The IaC
providers of each manage them as code.

## Consequences

- The Catalog route `PUT /api/catalog/policy/{namespace}` goes in slice 5.
- The IaC providers of RFC 48 get a resource for each object of decision 1.

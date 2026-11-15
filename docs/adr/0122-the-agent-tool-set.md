# 122. Twelve task-shaped tools, each with its action and object

Status: accepted · 2026-10-17. Decision 1 (the row `cancel_change`) narrowed by ADR 128. The `server:` action names superseded in part by ADR 210

Changed by:

- ADR 128 changes decision 1: the row cancel_change.
- ADR 210 changes: the route paths and the server: action names.
- ADR 367 changes decision 1: the test keeps the catalog at 8 to 15 tools.
- ADR 367 changes decision 14: the catalog can have more than 12 tools, for the
  three tools of the comment and propose change.

## Context

ADR 120 records the built-in agent, Dusty. The project owner decided on
about 8 to 12 task-shaped tools, not a passthrough of the raw API, and no
shell tool, ever. ADR 123 offers the same tools to outside agents.

ADR 17 says that each endpoint declares its actions and the object of each
action. A tool is an endpoint for an agent. So each tool declares its
action and its object in the same way.

A tool for each API route would give a model more than 100 tools. Each tool
costs context in each request, and a long list makes each choice of the
model harder. A task-shaped tool reads several objects and answers a shape
that a model can use.

The project manager decided on 2026-10-17, for the owner:

- The tools check the actions of the job model.
- Act-as means never more than the person.
- A read-only tool for live status joins the catalog, and `compare_states`
  merges into `read_state`, so the catalog stays at 12 tools.

## Decision

1. **The catalog.** Every channel offers these tools, in this order. Each
   tool checks its action on its object for the person of the call:

   | Tool | What it does | Action | Object | Effect |
   | --- | --- | --- | --- | --- |
   | `search_units` | finds units by name, tool, namespace, collection, label, owner or resource type | `unit:read` | the scope of the person | read |
   | `read_audit_log` | reads audit entries of a namespace, with filters | `namespace:read-audit` | namespace | read |
   | `explain_access` | shows who can do what on a unit, and through which grant | `unit:read-access` | unit | read |
   | `search_docs` | finds pages in the docs of this server version | `caller:read` | caller | read |
   | `describe_unit` | the context output, docs, links, owners, on-call, lock, last change, masked outputs, resource counts and the executors of a change | `unit:read` | unit | read |
   | `read_state` | the resources of a unit at one version, masked, with a filter. With `compareTo`: the resources added, deleted and changed since an older version | `unit:read` | unit | read |
   | `observe_unit` | the live status of the resources from the cloud: events of workloads, health of targets | `unit:observe` | unit | read |
   | `list_history` | updates, state versions, jobs and locks of a unit, newest first | `unit:read` | unit | read |
   | `read_run_output` | the events and the log of one update or job, masked | `job:read-logs` | unit | read |
   | `propose_change` | starts a change as a job (ADR 125) | the start actions of the job model: `unit:run-plan`, `unit:run-apply`, `unit:run-destroy` or `unit:write-back` | unit | propose |
   | `get_change` | the state of a change | `job:read` | the unit of the change | read |
   | `cancel_change` | cancels a change that did not end | `job:cancel` | the unit of the change | cancel |

   The code holds the catalog in `agent.Catalog`, with the parameters of
   each tool. A test keeps the catalog at 8 to 12 tools, checks that each
   tool has an action (or takes the start actions), checks the actions of
   the job tools, and checks that the parameter for its object is
   required.
2. **Act-as means never more than the person.** The action of each tool is
   the action that the same read or change needs in the API. `propose_change`
   takes its actions from the job model for the resolved plan
   (`Changes.StartActions`, which the job model answers from
   `job.StartRequirements` with no break-glass). The `Toolset` checks each
   of them for the person before the start, and the job model checks them
   again. A tool that reads job data checks the job action, never a wider
   one.
3. **Every call also checks `agent:use`.** Each tool call that touches an
   object in a namespace also checks the new action `agent:use` on that
   namespace. The built-in roles that read a namespace hold it. An
   administrator who wants no agent in a namespace uses a custom role
   without it. A list tool filters with the scope of `agent:use` and of
   its own action.
4. **The server checks before it acts.** The `Toolset` checks, in this
   order:
   1. the arguments (`agent.ValidateArguments`),
   2. for `propose_change` and a secret read, that the principal can sign
      in to the console,
   3. `agent:use`,
   4. the action of the tool,
   5. for a secret read, the secret action, the secret gate and, for an
      outside agent, the consent (decision 7).

   It resolves the person again just before each call (ADR 120 decision
   13). A denied call answers an error result to the model and
   writes an audit entry with the outcome `denied` (ADR 126).

   A principal that cannot sign in to the console, for example the
   principal of a service token (ADR 53 decision 7), can never pass the
   confirmation gate of a change (ADR 125 decision 5) or approve a consent
   (ADR 123 decision 10). So its `propose_change` and its secret read fail
   at once with their own error, `agent.ErrServicePrincipal`, before any
   read, consent or job. Its masked reads work as for a person.
5. **Parameters have bounds.** Each `agent.Param` has a type (`string`,
   `integer`, `boolean` or `array`), and a maximum: the bytes of a string,
   the value of an integer, the items of an array. A string can have an
   enum. An integer is 1 or more. The `Toolset` refuses a parameter that
   the tool does not have, a missing required parameter, a wrong type and a
   value outside the bounds, with `ErrInvalidArguments`, before any check of
   an action. The error names the parameter, never the value. The main
   bounds:

   | Parameter | Bound |
   | --- | --- |
   | `limit` of `search_units` | 1 to 100, default 20 |
   | `limit` of `list_history` | 1 to 100, default 20 |
   | `limit` of `read_audit_log` | 1 to 200, default 50 |
   | `limit` of `observe_unit` | 1 to 200, default 50 |
   | `limit` of `search_docs` | 1 to 20, default 5 |
   | `query` | 200 bytes |
   | names, references, versions, filters | 512 bytes |
   | `reason` | 2,000 bytes |
   | `edits` of `propose_change` | 20 file edits |
   | `tool` of `search_units` | `pulumi`, `opentofu` |
   | `what` of `observe_unit` | `events`, `health`, `all` |
   | `operation` of `propose_change` | `plan`, `apply`, `destroy`, `refresh` |
   | `via` of `propose_change` | `write-back`, `runner` |
   | `mode` of `propose_change` | `review-request`, `approve-first`, `direct` |
   | `consentID` of `read_state` | 512 bytes |

   `read_state` refuses `secretValues` with `compareTo`.
6. **Lists filter in the query.** `search_units` filters with
   `Authorizer.ListObjects`, never with one check for each object. It says
   when it left objects out, as a list route does (ADR 17).
7. **Secret values only through `read_state`, with one shape for a
   refusal.** All tools answer masked values (ADR 41). `read_state` takes
   `secretValues: true`. A call with it also checks `unit:read-secrets` on
   the unit, then the stages of the secret gate (`agent.SecretGate`, ADR
   124), then, for an outside agent, the consent for this one use. A test
   fails for any other tool that names a secret action.
   - **A refusal is a result, not an error.** When a check refuses, the
     call succeeds with a `StateResult` that holds the masked values and
     the closed stage in `closedStage`. The audit entry has the outcome
     `denied` and the stage.
   - **The consent fields.** When only the consent is missing, the
     `Toolset` makes a pending consent (`ConsentStore.Create`) and the
     result also holds `consentID` and `consentURL`: the console page
     (`agent.ConsentPath`) where the person approves it. A consent that
     still waits for approval is answered again, not made again. When an
     earlier stage is closed, the call makes no consent, because the
     person could not use it.
   - **The retry.** After the approval, the agent calls `read_state` again
     with the parameter `consentID`. Each tool that can reveal has this
     parameter, and a test fails for a tool with a secret action and no
     `consentID`. The `Toolset` finds the consent with
     `ConsentStore.Lookup`, bound to the person, the token, the MCP
     session, the tool and the unit of the call: an ID of another call
     answers as not found. It then uses the consent once
     (`ConsentStore.Use`). A used, expired or other consent gives the
     refusal with a new consent.
   - **After a reveal.** A token or an MCP session that used a consent
     cannot propose a change (`ConsentStore.Revealed`, ADR 124 decision
     12).

   The `Toolset` takes the `ConsentStore` as a dependency
   (`agent.ToolsetDeps`), with the read ports of decision 12, the
   `SecretGate`, the `RunStore` (for `Run.ReadPlainSecrets`) and the
   `ToolAuditor`.
8. **Live status with read-only, short-lived credentials.** `observe_unit`
   reads the cloud of the unit through `agent.LiveObserver`. For each call,
   the observer gets short-lived, federated, read-only credentials through
   the profile of the unit or the workload identity of the server, and
   drops them after the call. Dusty never holds a long-lived cloud key, and
   no tool takes one. A unit with no such identity answers
   `ErrObserveUnavailable`. The result names the identity that the server
   used, never a credential.
9. **No command tool.** No tool takes a command, a script, a query
   language or code to run. A test fails for a tool name or a parameter
   name that contains `shell`, `exec`, `command`, `script`, `eval`,
   `run_code` or `bash`.
10. **Results.** A tool answers JSON in the shapes of `pkg/agent/results.go`:

    | Tool | Result type |
    | --- | --- |
    | `search_units` | `SearchUnitsResult` |
    | `describe_unit` | `UnitResult` |
    | `read_state` | `StateResult` |
    | `observe_unit` | `ObserveResult` |
    | `list_history` | `HistoryResult` |
    | `read_run_output` | `RunOutputResult` |
    | `read_audit_log` | `AuditResult` |
    | `explain_access` | `AccessResult` |
    | `search_docs` | `DocsResult` |
    | `propose_change`, `get_change`, `cancel_change` | `ChangeResult` |
    | any failed call | `ToolError` |

    Each object in a result carries its reference, so the model can name
    it in its answer and the console can link it. Each list says when it
    left objects out. The loop cuts a result above the byte limit of a run
    (ADR 120 decision 16) and adds a note that says so. A tool error is a
    short message in the strict style of the API errors, with no value from
    the failure.
11. **Each tool keeps the words of each tool.** A result for a Pulumi stack
    says "stack", "update" and "preview". A result for an OpenTofu
    workspace says "workspace", "state version" and "plan". A tool reads
    each tool package through its own port and never merges the models
    (ADR 13).
12. **The read ports.** The `Toolset` reads through these ports. Each
    package that owns the data implements its port:

    | Port | Implemented by | Tools |
    | --- | --- | --- |
    | `UnitFinder` | the inventory | `search_units` |
    | `UnitReader` | each tool package: one for stacks, one for workspaces | `describe_unit`, `read_state`, `list_history`, `read_run_output` (updates) |
    | `JobReader` | the job model, with the person as the principal | `list_history` (jobs), `read_run_output` (jobs) |
    | `LiveObserver` | the cloud profiles | `observe_unit` |
    | `AuditReader` | the audit store | `read_audit_log` |
    | `AccessExplainer` | the authorizer | `explain_access` |
    | `DocsIndex` | the docs in the binary | `search_docs` |
    | `Changes` | the job model (ADR 125) | `describe_unit` (executors), `propose_change`, `get_change`, `cancel_change` |
    | `SecretGate` | the server, from its flag, its window and the opt-ins (ADR 124) | `read_state` (secret reads) |
    | `ConsentStore` | each SQL store (ADR 124) | `read_state` (consents of outside agents), `propose_change` (the reveal mark) |

13. **One `Toolset` for every channel.** The loop of Dusty, the MCP
    endpoint and the stdio proxy call one `agent.Toolset`. So every
    channel gets the same tools, the same checks and the same audit
    entries. The `Toolset` takes the actor of each call: the resolved
    person, the agent (`agent.Ref`: the channel, and the run or the MCP
    session) and the break-glass session flag. It answers an
    `agent.ToolOutput`: the content, the revealed values of a secret read,
    and the closed stage and consent of a refused one. `agenttest.RunToolset`
    checks the specs, the record of a read, a call without `agent:use`
    (denied, with a record), a failed audit write (the call fails), an
    unknown tool, the bounds of the arguments, the gate closed and open, the
    consent of an outside agent (no consent while an earlier stage is
    closed), an approved consent that reveals once and refuses a second
    use, a consent bound to its session, the refusal of a propose after a
    consent reveal (in the same session and in a new session with the same
    token), the refusal of a propose after a reveal of a run, and the
    service principal that cannot propose or read secret values.
14. **The catalog is a contract.** Outside agents depend on the tool names
    and parameters (ADR 123). A tool or a required parameter is never
    renamed or deleted without a deprecation. A new tool, an optional
    parameter or a new enum value can come at any time. A change that makes
    the catalog larger than 12 tools needs a new ADR. No release had
    `compare_states`, so its merge into `read_state` is not a deprecation.

### New actions

15. **The agent needs these actions.** Each one gets an entry in the
    action list of `pkg/identity` and in the role catalog:

    | Action | Object | Who holds it |
    | --- | --- | --- |
    | `agent:use` | namespace | the built-in roles that read a namespace |
    | `agent:start-run` | caller | every signed-in person while the agent is on |
    | `namespace:manage-agent-runs` | namespace | `namespace-admin` |
    | `namespace:manage-agent` | namespace | `namespace-admin` |
    | `server:manage-agent` | `@server` | `site-administrator` |
    | `server:accept-agent-secret-access` | `@server` | `site-administrator` only |
    | `caller:manage-agent-opt-ins` | caller | every signed-in person |
    | `unit:observe` | unit | the built-in roles that read a unit |

    The job actions (`job:read`, `job:read-logs`, `job:cancel` and the start
    actions) belong to the job model. Until these changes land,
    `pkg/agent/actions.go` declares the agent actions, and the same strings
    of the job actions.

## Consequences

- Each tool needs a read port in each package that it reads (decision 12).
  Most of these reads exist for the API already: the inventory, the masked
  state read, the state diff, the history of a unit and the audit list. The
  logs of a job come from the job model.
- `observe_unit` needs the cloud profiles of a unit, or a workload identity
  of the server. Without one, the tool answers that the unit has no
  read-only identity.
- `search_docs` needs the docs in the binary, with a small index. The docs
  of the server version answer, not the docs of the newest release.
- `agent:use` is one more check on each call of an agent. The authorizer
  can answer it from its cache (ADR 52).
- A model that needs a fact that no tool answers says so to the person. A
  new tool is a change of the catalog (decision 14), not a raw API call.

## Not decided here

- A tool that reads the OpenDustpan configuration of a namespace, for
  example its policies or its runners, after those features exist.
- A tool for drift (RFC 25) and for cost (RFC 45).

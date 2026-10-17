# 122. Twelve task-shaped tools, each with its action and object

Status: proposed · 2026-10-17

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

## Decision

1. **The catalog.** Every channel offers these tools, in this order. Each
   tool checks its action on its object for the person of the call:

   | Tool | What it does | Action | Object | Effect |
   | --- | --- | --- | --- | --- |
   | `search_units` | finds units by name, tool, namespace, collection, label, owner or resource type | `unit:read` | the scope of the person | read |
   | `read_audit_log` | reads audit entries of a namespace, with filters | `namespace:read-audit` | namespace | read |
   | `explain_access` | shows who can do what on a unit, and through which grant | `unit:read-access` | unit | read |
   | `search_docs` | finds pages in the docs of this server version | `caller:read` | caller | read |
   | `describe_unit` | the context output, docs, links, owners, on-call, lock, last change, masked outputs, resource counts and the kinds of change | `unit:read` | unit | read |
   | `read_state` | the resources of a unit at one version, masked, with a filter | `unit:read` | unit | read |
   | `compare_states` | the resources added, deleted and changed between two versions | `unit:read` | unit | read |
   | `list_history` | updates, state versions, jobs and locks of a unit, newest first | `unit:read` | unit | read |
   | `read_run_output` | the events and output of one update or job, masked | `unit:read` | unit | read |
   | `propose_change` | proposes a change to a unit (ADR 125) | `unit:write`, and the action of the kind of change | unit | propose |
   | `get_change` | the state of a change | `unit:read` | the unit of the change | read |
   | `cancel_change` | cancels a change that did not start to apply | `unit:write` | the unit of the change | cancel |

   The code holds the catalog in `agent.Catalog`, with the parameters of
   each tool. A test keeps the catalog at 8 to 12 tools, checks that each
   tool has an action, and checks that the parameter for its object is
   required.
2. **Every call also checks `agent:use`.** Each tool call that touches an
   object in a namespace also checks the new action `agent:use` on that
   namespace. The built-in roles that read a namespace hold it. An
   administrator who wants no agent in a namespace uses a custom role
   without it. A list tool filters with the scope of `agent:use` and of
   its own action.
3. **The server checks before it acts.** A tool resolves its object from
   its arguments, and checks `agent:use` and its action before it reads or
   changes anything. A denied call answers an error result to the model
   and writes an audit entry with the outcome `denied` (ADR 126).
4. **Lists filter in the query.** `search_units` filters with
   `Authorizer.ListObjects`, never with one check for each object. It says
   when it left objects out, as a list route does (ADR 17).
5. **Secret values only through `read_state`.** All tools answer masked
   values (ADR 41). `read_state` takes `secretValues: true`. A call with it
   also checks `unit:read-secrets` on the unit and then the secret gate
   (ADR 124). When either one refuses, the call answers the masked values
   and says why. A test fails for any other tool that names a secret
   action.
6. **No command tool.** No tool takes a command, a script, a query
   language or code to run. A test fails for a tool name or a parameter
   name that contains `shell`, `exec`, `command`, `script`, `eval`,
   `run_code` or `bash`.
7. **Results.** A tool answers JSON. Each object in a result carries its
   reference, so the model can name it in its answer and the console can
   link it. The loop cuts a result above the byte limit of a run (ADR 120
   decision 16) and adds a note that says so. A tool error is a short
   message in the strict style of the API errors, with no value from the
   failure.
8. **Each tool keeps the words of each tool.** A result for a Pulumi stack
   says "stack", "update" and "preview". A result for an OpenTofu
   workspace says "workspace", "state version" and "plan". A tool reads
   each tool package through its own port and never merges the models
   (ADR 13).
9. **The kinds of change come from the job model.** `describe_unit`
   answers the kinds of change that the job model offers for the unit,
   with the shape of the spec of each kind. `propose_change` takes one of
   them. The agent package does not define kinds of change (ADR 125).
10. **One `Toolset` for every channel.** The loop of Dusty, the MCP
    endpoint and the stdio proxy call one `agent.Toolset`. So every
    channel gets the same tools, the same checks and the same audit
    entries. The `Toolset` takes the actor of each call: the resolved
    person, the channel, the run or the MCP session, and the break-glass
    flag.
11. **The catalog is a contract.** Outside agents depend on the tool names
    and parameters (ADR 123). A tool or a required parameter is never
    renamed or deleted without a deprecation. A new tool or an optional
    parameter can come at any time. A change that makes the catalog
    larger than 12 tools needs a new ADR.

### New actions

12. **The agent needs these actions.** Each one gets an entry in the
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

    Until that change lands, `pkg/agent` declares them as constants.

## Consequences

- Each tool needs a read port in each tool package that it reads. Most of
  these reads exist for the API already: the inventory, the masked state
  read, the state diff, the history of a unit and the audit list. The
  events of a job come from the job model.
- `search_docs` needs the docs in the binary, with a small index. The docs
  of the server version answer, not the docs of the newest release.
- `agent:use` is one more check on each call of an agent. The authorizer
  can answer it from its cache (ADR 52).
- A model that needs a fact that no tool answers says so to the person. A
  new tool is a change of the catalog (decision 11), not a raw API call.

## Not decided here

- A tool that reads the OpenDustpan configuration of a namespace, for
  example its policies or its runners, after those features exist.
- A tool for drift (RFC 25) and for cost (RFC 45).

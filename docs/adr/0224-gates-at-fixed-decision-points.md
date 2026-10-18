# 224. Gates at fixed decision points

Status: accepted · 2026-10-18. Part of ADR 222. Supersedes in part ADR 17 ("A policy check on every change"). Supersedes ADR 103 decision 1, ADR 112 decisions 6 and 7, and ADR 113 decisions 8 and 9 in part. Supersedes ADR 114, except decision 4 and the request signature of decision 8. Supersedes the `expr` rules of ADR 115 decision 2, and ADR 201 decision 11

## Context

ADR 17 asks for a policy check on each change, with the outcomes deny and
wait for approval. ADR 114 gives a port with four adapters and the kinds
`catalog.request` and `job.start`. The job decision runs once, before the
plan (ADR 103 decision 1), so no rule can read the plan or its cost. The
Catalog checks again at three recheck points (ADR 113 decision 8). Profiles
run Expr `checks` at save and open (ADR 201 decision 11). None of these has a
warn outcome or a mode that records a result without an effect.

The spike made each gate one Cedar `permit` policy, with the gate ID as the
policy ID. One Cedar request for each unit gave the matched gates as the
determining policies of `cedar-go`. With 3 gates, one evaluation for one
unit took 98 µs on 4 CPUs of an Intel Xeon at 2.1 GHz, with no cache.

## Decision

1. **A gate (owner).** A gate is a stored object with these fields:
   - `id`: unique in its scope. The Cedar policy ID is
     `<scope key>/<id>`. The scope key is `installation`,
     `namespace/<namespace>`, `collection/<namespace>/<collection>`,
     `unit/<namespace>/<tool>/<name>`, `blueprint/<blueprint>/<version>` or
     `profile/<namespace>/<profile>`. Each variable segment is
     percent-encoded (RFC 3986), so a segment never holds `/`. For example,
     the Pulumi stack `web/prod` of `acme` has the scope key
     `unit/acme/pulumi/web%2Fprod`. A gate ID matches
     `[a-z0-9][a-z0-9-]{0,62}`, and only the server makes the IDs with the
     reserved prefix `builtin/`. So two scopes can each hold a gate with the
     same ID, and the record names each gate by its policy ID.
   - `scope`: the installation, a namespace, a collection or a unit. The
     route path names it (ADR 230 decision 3). A blueprint version or a
     profile can also hold gates (decision 5).
   - `decisionPoint`: one of decision 2.
   - `condition`: the body of the `when` clause of one Cedar policy over the
     fact document of the decision point. Empty means true.
   - `unless`: an optional body of an `unless` clause (decision 6).
   - `outcome`: `deny`, `approval:<rule>` or `warn`, as data.
   - `message`: the text that the record and the client show. It can name
     facts as `{{fact.path}}` (decision 11).
   - `mode`: `enforce` or `evaluate` (decision 7).
   - `onError`: `fail` (the default) or `skip` (decision 13).
   - `final`: a flag (decision 12).
2. **Six decision points (owner).**

   | Decision point | When | Replaces |
   | --- | --- | --- |
   | `change.propose` | before a job, a change order or a Catalog revision starts, before a plan | `job.start`, `catalog.request` at `submit` |
   | `change.planned` | after the plan is stored and its facts are ready | `changeorder.review`, the request of ADR 144 decision 14 |
   | `change.apply` | before the dispatch of each phase that changes | `catalog.request` at `apply` |
   | `profile.save` | before a save, a rollback, a tag move or a draft apply of a profile | the change rule of ADR 202 decision 6 |
   | `profile.open` | before each open of a profile, direct or by a job | `checks` at open, the open rule of ADR 202 decision 7 |
   | `state.write` | before a write of state that no job of OpenDustpan makes (decision 16) | no earlier check |

   Each decision point has its own fact schema (ADR 226). A new decision
   point is a new ADR. The owner added `state.write` on 2026-10-18.
3. **The recheck at `change.apply`.** `change.apply` evaluates the gates
   again with the facts of that moment. A recheck only adds. A new `deny`
   refuses the dispatch. A new requirement puts the change back into the
   approval wait. A signature that meets a requirement of the new result
   still counts when its signer is still an eligible approver (ADR 225
   decision 7), is not disabled and has no revoked credential. A gate that matched before and does not match now removes
   nothing that a person already gave. The plan staleness check of ADR 191
   decision 7 runs at `change.apply` before the gates, with the maximum plan
   age of ADR 102. A stale plan stops the dispatch before any gate runs.
4. **One Cedar request for each unit (owner).** The server evaluates all the
   gates of a decision point for one unit in one Cedar request. The
   principal is the subject of the change, the action is
   `OpenDustpan::Action::"<decision point>"`, the resource is the unit and
   the context is the fact document. The determining policies are the
   matched gates. A change of several units gives one request for each unit
   and one decision record for the change (ADR 229).
5. **Which gates apply.** The gates of a unit are the gates of the
   installation, of its namespace, of each collection that holds it, of the
   unit, and the gates that a blueprint version or a profile holds when the
   change uses it. A blueprint `validate` or `policy` rule of ADR 112 becomes
   a gate of the blueprint version at `change.propose`. A profile `check`
   becomes a gate of the profile at `profile.open`. A gate of a profile holds
   for each open of a profile that imports it. No profile removes the gate
   of another profile (ADR 201 decision 3 stays).
6. **Exemptions with `unless` (owner).** A wider gate carves out its own
   exemptions in its `unless` clause. No outcome relaxes another outcome. A
   narrower scope cannot exempt a change from a wider gate.
7. **Modes.** A gate in `enforce` mode gives its outcome. A gate in
   `evaluate` mode is evaluated and written in the record, with no effect.
   A new gate starts in `evaluate` mode unless the request names `enforce`.
8. **Check at save (PM).** The server parses and type-checks each gate at
   save against the fact schema of its decision point, with the schema
   validator of `cedar-go`. The server parses `condition` and `unless` as
   one Cedar expression each, and builds the policy from the parsed form,
   never by a splice of text. It refuses a gate that does not parse, that
   holds a second policy, that names an unknown fact or attribute, or that
   has a type error, with `gate.ErrInvalid`. The message names the gate and
   the Cedar diagnostic. CI also runs the validator of the Rust Cedar
   implementation through the image of the external Cedar service, on
   every fact schema and every built-in gate and starter pack.

   When the external adapter is configured (decision 9), the save also
   calls the check operation of the service, and a refusal there
   refuses the save. When the service does not answer, a save that
   switches a gate to `evaluate`, removes it or sets `onError: skip` goes
   on with the linked validator only, and the audit entry says so. Every
   other save fails with `gate.ErrUnavailable`. After an upgrade of `cedar-go` or of a fact schema,
   the server checks each stored gate again at start. A gate that fails
   this check has the state `invalid`. An `invalid` gate in `enforce` mode
   gives `gate.ErrRuleFailed` at each evaluation, as decision 13 says,
   until a person saves it again.

   Benchmark: the `cedar-go` schema validator lives under `x/exp` with no
   compatibility promise, and the Rust validator is the reference. AWS
   Verified Permissions refuses a policy that fails its schema at save.
9. **One deep engine port, two adapters (owner).** The port `gate.Engine`
   takes the compiled policy set of a decision point, the entities and one
   request for each unit. It gives, for each unit, the matched policy IDs and
   the diagnostics. The server links `cedar-go` as the default adapter. A
   second adapter calls an external Cedar service over HTTP. An
   installation names one adapter for gates.

   The external adapter authenticates the service (TLS with a configured
   CA, and a credential). Each answer echoes the request ID and the version
   of the policy set. An answer that does not authenticate, does not echo
   both, or names an unknown policy ID gives `gate.ErrUnavailable`. Access
   limits (ADR 223) always use the linked adapter, so an outage of the
   service never refuses every API call. The port has a conformance suite,
   `pkg/gate/gatetest`, that each adapter runs. A later engine is a new
   adapter of the same port and a new ADR.
10. **The action is pinned (PM).** Every gate policy carries
    `action == OpenDustpan::Action::"<decision point>"`. The server adds the
    pin to the parsed policy. A condition never names an action.

    Benchmark: the spike found that a policy with no action is checked
    against each action of the schema, and fails for a wrong reason.
11. **`has` is refused on a schema attribute (PM).** The save refuses `has`
    on an attribute that the fact schema names, with `gate.ErrInvalid`. An
    optional fact has an `available` flag (ADR 226 decision 3). A message
    template that names a path that is not a fact is refused at save. A
    template that cannot render at run time stops the change with
    `gate.ErrRuleFailed` and never gives an empty string.

    Benchmark: in the spike, `context has risk && context.risk.score > 3`
    passed the type check and never matched, so the gate stopped nothing.
12. **Final gates (owner, 2026-10-18).** A narrower scope never changes the
    mode, the condition, the `unless`, the outcome or `onError` of a wider
    gate, final or not. Only a holder of the manage action of the gate's own
    scope changes or removes a gate. `final: true` adds these effects. Each
    gives `gate.ErrFinal` and changes nothing:
    1. A save at a narrower scope of a gate with the ID of a final gate of a
       wider scope.
    2. A save at a narrower scope of an approval rule with the ID of the
       approval rule that a final gate names. ADR 225 decision 1 refuses
       this for each rule of a wider scope.
    3. A break-glass for a gate in error (decision 13) that a holder of the
       break-glass action of a narrower scope asks for, when the gate is
       final. Only the break-glass action of the final gate's own scope
       passes it.
    4. A change at the gate's own scope that makes a final gate weaker, with
       no `reason` in the request. Weaker means: a switch to `evaluate`, a
       removal, a clear of `final`, or a change of `decisionPoint`,
       `condition`, `unless`, `outcome` or `onError`. A change of the
       approval rule that a final gate names, or of a path set that it
       reads, counts as a change of the gate.

    The owner of the gate's own scope can make a final gate weaker. This is
    a change-controlled edit: it needs the manage action of that scope, a
    `reason`, a fresh authentication (ADR 227) in each mode, and it writes an
    audit entry of the kind `change` that names the gate as final, the old
    and the new version and the reason. The console shows the change in the
    access review of the scope (ADR 52).

    An approval default and an ownership rule only add requirements, and
    their `unless` exempts a change from their own requirement only. So no
    default and no ownership rule conflicts with a final gate, and the save
    check finds none.

    Benchmark: GitHub organization rulesets hold for each repository, and a
    repository administrator cannot exempt a repository. GitLab security
    policy projects keep the policy out of reach of the project
    maintainers. AWS service control policies hold for each member account,
    and only the management account changes them. SOC 2 CC8.1 and ISO/IEC
    27001:2022 Annex A 8.32 ask that each change of a control is
    authorized, recorded and has a reason.
13. **An error fails closed (owner).** A diagnostic error of the engine
    for a gate in `enforce` mode stops the change with
    `gate.ErrRuleFailed`. It is never an allow and never a deny. The record
    has the result `error` and the Cedar message. An error of a gate in
    `evaluate` mode is in the record and stops nothing. An engine that does
    not answer stops the change with `gate.ErrUnavailable`.

    **`onError: skip` (owner, 2026-10-18).** The author of a gate can set
    `onError: skip` in advance. An error of such a gate, or an engine that
    does not answer for it, then counts as no match. The record shows the
    gate with the result `error`, the diagnostic and `skipped`. The default
    is `fail`.

    **A break-glass for gates in error (owner, 2026-10-18).** A person can
    pass the gates whose result is `error` or `gate.ErrUnavailable` for one
    change, at one evaluation:
    - It needs the action `namespace:break-gate-error` on the namespace of
      the change. That action passes each gate in error that holds for the
      change, except a final gate of the installation. A final gate of the
      installation needs `installation:break-gate-error` (decision 12, ADR
      230 decision 2).
    - It needs a reason, a person in a session (never an agent, a service
      principal or a token) and a fresh authentication (ADR 227).
    - It passes only the gates in error. It never passes a `deny`, a
      requirement or a fixed control. The evaluation of the other gates
      stands.
    - It writes an audit entry of the kind `override`, and the decision
      record names it. The break-glass review of ADR 142 holds for it.
    - The change still meets `change.apply`. A gate that is still in error
      there needs a new break-glass.

    A change that a gate error stops waits in the approval wait with the
    cause `gate-error` (the job state value stays `awaiting-approval`), for
    at most its timeout. A break-glass on its record evaluates the decision
    point again and passes the gates that are still in error. For
    `state.write`, a break-glass gives the person a pass of the same
    principal, unit and `operation.kind` for 15 minutes, used at most once.
    The record of the next write names the break-glass.

    Benchmark: the Kubernetes admission webhook `failurePolicy` (`Fail` or
    `Ignore`) and the GitLab `fallback_behavior` of a policy are set by the
    author in advance. A freeze has a break-glass (ADR 103 decision 6) with
    the review of ADR 142.
14. **Combining (owner).** For each unit, in this order:
    1. Any `deny` gives `deny`, with the message of each `deny`.
    2. Else each named approval rule gives one requirement, and the change
       waits for all of them, from distinct people (ADR 225).
    3. Else each `warn` gives `warn`.
    4. Else the result is `allow`.

    A change of several units takes the strictest result of its units.
    Requirements of all decision points of one change add up on the change.
    The change waits for them before the first phase that changes, after the
    plan, so approvers sign a plan. A requirement from `change.propose`
    never blocks the plan phase.
15. **Cache (PM).** The server caches each compiled policy set by gate ID and
    gate version. A save gives a new version. The record holds the version
    of each gate that it evaluated.

    Benchmark: in the spike, the parse of each gate at each call was most of
    the 6 to 17 µs that each gate added.
16. **`state.write`: writes from a person's own CLI (owner, 2026-10-18).**
    Version 1 evaluates the gates of `state.write` before each of these
    writes:

    | Write | When the point runs | `operation.kind` |
    | --- | --- | --- |
    | a Pulumi update that a person's own Pulumi CLI creates | at the create of the update, before the CLI starts it | `apply`, `destroy`, `refresh` or `import` |
    | a Pulumi stack import, restore or rename, or a `pulumi stack rm`, from a person's own Pulumi CLI | before its transaction | `state-import`, `rollback`, `rename` or `delete` |
    | a Pulumi update cancel from a person's own Pulumi CLI, which ends the update of another holder | before its transaction | `unlock` |
    | an OpenTofu write from a person's own CLI or CI, through the TFE-compatible API or the HTTP backend | at the lock of the workspace or of the state | `lock` |
    | an OpenTofu restore or force-unlock through the TFE-compatible API or the HTTP backend | before its transaction | `rollback` or `unlock` |
    | an OpenTofu workspace removal (a `DELETE` of the workspace) through the TFE-compatible API | before its transaction | `delete` |
    | an OpenTofu state version with no lock through the HTTP backend (the unlocked override, ADR 44) | before its transaction | `unlocked-write` |
    | a tool-neutral state operation | before its transaction | `rollback`, `force-write`, `state-import`, `delete` or `unlock` |

    The Pulumi import route carries `pulumi stack import`, `pulumi state
    delete`, `pulumi state unprotect`, `pulumi state move` and `pulumi state
    rename`. An UNLOCK of the HTTP backend by a principal that does not hold
    the lock is a force-unlock.

    Every route of a tool-compatible API that writes state, ends a lock of
    another holder or removes a unit meets `state.write`. The route test
    fails for a write route with no decision point. A Pulumi preview writes
    no state and meets no gate. A create of a unit with no state, a change
    of the override switches and a label change write no state. So
    `PATCH .../tags` of the Pulumi Cloud-compatible API meets no
    `state.write` (ADR 223 decision 10). The route test lists these routes
    as routes that write no state.

    A write with the job token of a phase that passed `change.apply`, to a
    unit of that job, meets no `state.write` evaluation, because its change
    met `change.propose`, `change.planned` and `change.apply`. The job token
    of any other phase gets no state write except the lock or the update
    lease of its plan (ADR 213 decision 2 holds for each plan phase). Any
    other write with it fails with `identity.ErrDenied`.

    **Facts with no plan.** The fact schema `state.write/1.0` holds the
    groups `principal`, `source`, `unit`, `operation` and `state`
    (ADR 226 decision 4). `plan.available` is always `false`. The `state`
    group has facts only for a tool-neutral operation, where the server
    holds both the current and the new state: `state.resources` (resources
    in the new state) and `state.removed` (resource addresses in the
    current state and not in the new state). For a CLI write,
    `state.available` is `false`.

    **The OpenTofu point runs at the lock.** OpenTofu writes a state
    version after the apply changed the resources. A refusal then leaves
    resources that no stored state records. So the point runs at the lock,
    before a plan or an apply. The lock request does not say whether a plan
    or an apply follows, so a gate at `state.write` stops both. The server
    never refuses a state version that holds a lock which passed
    `state.write`. A forced write with a lock (the forced override, ADR 44)
    still meets `state.write` at that lock. A state version with no lock
    (the unlocked override, ADR 44) meets `state.write` before its
    transaction, with `operation.kind` `unlocked-write`, and a refusal
    refuses the write. The refusal can leave resources that no stored state
    records. The record and the error say so.

    **Outcomes.** A `deny` refuses the write. A CLI cannot wait for people,
    so an approval outcome also refuses the write. Its message names the
    command of the OpenDustpan CLI that opens a change order for the unit.
    A `warn` lets the write go on and is in the record only. An error
    follows decision 13.

    **The refusal reaches each CLI in its own error shape.** Every refusal
    on a tool path answers `403`, so that no CLI reads it as a lock conflict
    or retries it. The message holds the gate message, the error code
    (ADR 230 decision 4) and the record ID.

    | Path | Shape |
    | --- | --- |
    | Pulumi Cloud-compatible API | `{"code": 403, "message": "..."}`. The Pulumi CLI prints the message and creates no update. |
    | TFE-compatible API | a JSON:API errors document with `status` `"403"`, a `title` and the message as `detail`. OpenTofu stops and holds no lock. |
    | OpenTofu HTTP backend | `403` with the message as a plain-text body. OpenTofu stops and holds no lock. |
    | tool-neutral API | the error of ADR 230 decision 4 |

    A CLI might show only the status code and not the message. The e2e
    step records the text that each supported CLI version shows, and the
    user documentation names the record route for that case.

    **e2e steps first.** Each path lands with its e2e step against the real
    CLI (`make e2e`). Each step saves a `deny` gate at `state.write` in
    `enforce` mode and checks that the CLI exits with an error that shows
    the gate message and the record ID, and that the state does not change.
    For OpenTofu, the step also checks that the workspace is not locked
    after the refusal. A second step saves the gate in `evaluate` mode and
    checks that the write goes on and writes a record. A third step saves an
    approval outcome and checks the message that names the command.
17. **Break-glass accounts meet the gates.** A session of a break-glass
    account (ADR 70) is outside the access limits (ADR 223 decision 7).
    Gates, approval rules and fixed controls hold for each change of such a
    session.

## Consequences

- With no gate and no approval default, every change is allowed, as today
  for a job (ADR 103, Consequences).
- A gate at the installation scope that errors stops each change below it,
  unless its author set `onError: skip`. The check at save and the simulate
  route (ADR 229) are required steps before `enforce`. The break-glass of
  decision 13 passes such a gate for one change.
- A team that runs `pulumi up` or `tofu apply` on a laptop meets the gates
  of `state.write`. A gate that needs a plan fact refuses such a write,
  because `plan.available` is `false`.

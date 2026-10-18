# 224. Gates at fixed decision points

Status: accepted · 2026-10-18. Part of ADR 222. Supersedes in part ADR 17 ("A policy check on every change"). Supersedes ADR 103 decision 1, ADR 112 decisions 6 and 7, and ADR 113 decisions 8 and 9 in part. Supersedes ADR 114, the `expr` rules of ADR 115 decision 2, and ADR 201 decision 11

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
   - `id`: unique in the installation, and the Cedar policy ID.
   - `scope`: the installation, a namespace, a collection or a unit. A
     blueprint version or a profile can also hold gates (decision 5).
   - `decisionPoint`: one of decision 2.
   - `condition`: the body of the `when` clause of one Cedar policy over the
     fact document of the decision point. Empty means true.
   - `unless`: an optional body of an `unless` clause (decision 6).
   - `outcome`: `deny`, `approval:<rule>` or `warn`, as data.
   - `message`: the text that the record and the client show. It can name
     facts as `{{fact.path}}` (decision 11).
   - `mode`: `enforce` or `evaluate` (decision 7).
   - `final`: a flag (decision 12).
2. **Five decision points (owner).**

   | Decision point | When | Replaces |
   | --- | --- | --- |
   | `change.propose` | before a job, a change order or a Catalog revision starts, before a plan | `job.start`, `catalog.request` at `submit` |
   | `change.planned` | after the plan is stored and its facts are ready | `changeorder.review`, the request of ADR 144 decision 14 |
   | `change.apply` | before the dispatch of each phase that changes | `catalog.request` at `apply` |
   | `profile.save` | before a save, a rollback, a tag move or a draft apply of a profile | the change rule of ADR 202 decision 6 |
   | `profile.open` | before each open of a profile, direct or by a job | `checks` at open, the open rule of ADR 202 decision 7 |

   Each decision point has its own fact schema (ADR 226). A new decision
   point is a new ADR.
3. **The recheck at `change.apply`.** `change.apply` evaluates the gates
   again with the facts of that moment. A recheck only adds. A new `deny`
   refuses the dispatch. A new requirement puts the change back into the
   approval wait. A signature that meets a requirement of the new result
   still counts. A gate that matched before and does not match now removes
   nothing that a person already gave.
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
   validator of `cedar-go`. It refuses a gate that does not parse, that
   holds a second policy, that names an unknown fact or attribute, or that
   has a type error, with `gate.ErrInvalid`. The message names the gate and
   the Cedar diagnostic. CI also runs the validator of the Rust Cedar
   implementation through the image of the external Cedar service, on
   every fact schema and every built-in gate and starter pack.

   Benchmark: the `cedar-go` schema validator lives under `x/exp` with no
   compatibility promise, and the Rust validator is the reference. AWS
   Verified Permissions refuses a policy that fails its schema at save.
9. **One deep engine port, two adapters (owner).** The port `gate.Engine`
   takes the compiled policy set of a decision point, the entities and one
   request for each unit. It gives, for each unit, the matched policy IDs and
   the diagnostics. The server links `cedar-go` as the default adapter. A
   second adapter calls an external Cedar service over HTTP. An
   installation names one adapter. The port has a conformance suite,
   `pkg/gate/gatetest`, that each adapter runs. A later engine is a new
   adapter of the same port and a new ADR.
10. **The action is pinned (PM).** Every gate policy carries
    `action == OpenDustpan::Action::"<decision point>"`. The server adds the
    pin to the policy text. A condition never names an action.

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
12. **Final gates.** A gate with `final: true` stops a narrower scope from
    saving a gate with the same ID. A narrower scope never changes the mode,
    the condition or the outcome of a wider gate. RFC 77 holds the open
    question of what more `final` means.
13. **Any Cedar error stops the change (owner).** A diagnostic error of the
    engine for any gate in `enforce` mode stops the change with
    `gate.ErrRuleFailed`. It is never an allow and never a deny. The record
    has the result `error` and the Cedar message. An error of a gate in
    `evaluate` mode is in the record and stops nothing. An engine that does
    not answer stops the change with `gate.ErrUnavailable`.
14. **Combining (owner).** For each unit, in this order:
    1. Any `deny` gives `deny`, with the message of each `deny`.
    2. Else each named approval rule gives one requirement, and the change
       waits for all of them, from distinct people (ADR 225).
    3. Else each `warn` gives `warn`.
    4. Else the result is `allow`.

    A change of several units takes the strictest result of its units.
15. **Cache (PM).** The server caches each compiled policy set by gate ID and
    gate version. A save gives a new version. The record holds the version
    of each gate that it evaluated.

    Benchmark: in the spike, the parse of each gate at each call was most of
    the 6 to 17 µs that each gate added.
16. **No decision point on a tool API call.** A state write or a CLI-run
    update through a tool-compatible API passes the access check and the
    access limits. It meets no gate in version 1, as a freeze never refuses
    a state write (ADR 103 decision 5). RFC 77 holds the question.

## Consequences

- With no gate and no approval default, every change is allowed, as today
  for a job (ADR 103, Consequences).
- A gate at the installation scope that errors stops each change below it.
  The check at save and the simulate route (ADR 229) are required steps
  before `enforce`. RFC 77 holds the break-glass question.

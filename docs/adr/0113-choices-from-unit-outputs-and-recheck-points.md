# 113. Choices from unit outputs, and the recheck points

Status: accepted · 2026-10-17. Route paths superseded in part by ADR 210

## Context

The owner decided on 17 October 2026: choices come from unit outputs.
Dependent choices use equality filters, for example a subnet group that
depends on the chosen network. The server alone computes the options, and
checks the answers at submit, at approval and at apply, because values can
change between them.

ADR 41 lets the server read state and mask secret values. ADR 153 records
which dependents read the outputs of a unit.

## Decision

1. **A choice has static values or a source.** An input with a `choice`
   (ADR 112 decision 5) gets its options in one of two ways:
   - `values`: a fixed list of 1 to 1,000 strings in the blueprint version.
   - `source`: the outputs of a set of units, at read time.
2. **A source is a set of units and two outputs.** `catalog.Source` names:
   - the units: the units made from one blueprint of the namespace
     (`blueprint`), or the units of one collection (`collection`). Exactly one
     of the two.
   - `value`: the output that gives the value of an option. Its value must be
     a string, a number or a boolean. The option value is the string form of
     it.
   - `label`: the output that gives the text that a form shows, or empty to
     show the value.
   - `where`: 0 to 8 equality filters (decision 4).

   An output reference (`catalog.OutputRef`) names a top-level output, and
   can name a value in it with a JSON pointer (`path`). A unit whose value
   output is missing, is not a scalar, or is secret gives no option. An
   option never shows a secret output: the read masks it, and the engine
   skips a masked value.
3. **Only units with a state version count.** A unit gives options from the
   outputs of its last state version. A unit with no state version, a
   deleted unit (ADR 47) and the unit of the request itself give no option.
4. **Filters are equality only.** A filter (`catalog.Filter`) names an
   output and the value that it must equal: a fixed JSON scalar, or the
   answer of another input (`{"input": "<name>"}`). Two values are equal
   when their canonical JSON (ADR 111 decision 6) is equal, so `"1"` does
   not equal `1`, and `1` equals `1.0`. A filter
   on an input makes the choice depend on that input. There is no other
   operator in version 1.
5. **Dependencies form a list, not a loop.** The inputs and their filters
   form a graph. A publish refuses a loop with `catalog.ErrChoiceCycle`, and
   a chain of more than 8 dependent choices with `catalog.ErrLimit`.
   `catalog.Order` gives the inputs in an order where each input comes after
   the inputs that it depends on. A form asks in that order.
6. **The options port.** The Catalog reads outputs through
   `catalog.OutputSource`. Its one method, `Outputs`, gets a namespace, the
   units of decision 2 and the output names, and returns one
   `catalog.UnitOutputs` for each unit: the unit reference with its unit ID,
   the state version, and each output asked for as JSON, with its secret
   mark. The internal wiring implements it over the masked read of the tool
   packages (ADR 41). It returns `catalog.ErrSourceUnavailable` when it
   cannot read, and never a partial list as a full one.
7. **The engine is pure.** `catalog.Options` takes an input, the answers so
   far and the rows of the port, and gives every option, sorted by label,
   then by value, with no limit. When an input that the choice depends on
   has no answer, the result is empty, and `catalog.OptionSet.WaitingFor`
   names those inputs. When two units give the same value, the options
   merge into one, with the labels of both. `catalog.CheckAnswer` gives a
   failure when an answer is not among the options. It always takes the
   full set: `OptionSet.ForRoute` cuts a set at 1,000 options for the
   options route, and `CheckAnswer` refuses a cut set with
   `catalog.ErrInvalid`, because an answer past the cut is still an option.
   Both are pure, and table-driven tests cover them.
8. **Recheck points.** The server checks a revision again at each point
   where a value can change. `catalog.RecheckPoint` names them:

   | Point | When | What fails |
   | --- | --- | --- |
   | `submit` | the API takes a submit | the API answers 422 with the failures, and stores no revision |
   | `approval` | before the job records an approval (ADR 110, "Changes requested in the job model", item 2) | the job model rejects the job with the failures |
   | `apply` | before the dispatch of a phase that changes, and at each render for the team's CI | the job model rejects the job with the failures, or the render answers 422 |

   At each point the check runs in this order, and returns every failure of
   each step that runs:
   1. the document (ADR 111 decision 2) and the blueprint version: not
      retired at `submit`, and its Expr version (ADR 112 decision 11),
   2. the types and the Expr rules of each answer (ADR 112 decision 6),
   3. the choices, with fresh options from the port,
   4. the policy (ADR 114).
   5. the quotas (ADR 115). A reservation that the revision holds counts
      as its own, so a recheck of a create does not count it twice.

   A step that cannot run is not a failure and never a pass: a policy that
   times out (`policy.ErrUnavailable`), a policy rule or a quota `expr` rule
   that fails to run (`policy.ErrRuleFailed`, `quota.ErrRuleFailed`), a
   source that does not answer (`catalog.ErrSourceUnavailable`), or a source
   principal that lost its grant (decision 10). The check stops at that
   step, returns the error, and nothing changes. At `submit` the API
   answers with the status 503. At `approval` and at `apply` the job model
   records no approval and dispatches nothing (ADR 110, "Changes requested
   in the job model", item 2): the job waits, the reconcile schedule asks
   again, and the timeout of the gate still ends a job whose check keeps
   failing. At `approval` and at `apply`, the `catalog.Rechecker` compares
   each requirement of the policy decision with its gate: the group set and
   the count of that requirement, never a total count of approvals.
9. **A recheck can only refuse.** A recheck never changes an answer, never
   picks another option, and never changes a gate. When a policy at a later
   point asks for a requirement that no gate of the job has, or for a larger
   count or a different group set than its gate has, the
   recheck fails with `catalog.ErrPolicyChanged`, and the requester submits
   again. So a person who approved saw the same answers as the apply uses.
10. **The server reads options as the source principal of the blueprint.**
    A requester sees the options of a source, also when it cannot read the
    source units. A blueprint with a source names a service principal in
    `sourcePrincipal` (ADR 112 decision 3), and the server reads the outputs
    of each source with the grants of that principal, never with the grants
    of the publisher at the time of the publish:
    - The principal is a principal of the namespace that an administrator
      makes. The blueprint version records it, so a reviewer of the
      blueprint sees who reads the sources.
    - A publish that names it needs `principal:manage-tokens` on it (ADR 117
      decision 2). A holder of that action can already act as the principal,
      so the publish gives the blueprint no grant that the publisher could
      not use itself.
    - At each computation of the options, at the options route and at each
      recheck, the server checks `unit:read` of the principal on the
      collection of each source, or on the namespace for a source of the
      units of a blueprint (`catalog.SourceRequirements`). A revoke of the
      grant stops the reads at once: the options route answers with the
      error, and a recheck cannot run.

    An option shows only its value and its label, never another output, the
    state or a unit that the source does not name. The audit entry of the
    publish names each source and the principal.
11. **The options route.** `POST /api/catalog/options/{namespace}` (ADR 117)
    takes a blueprint version, the answers so far, and, for an update, the
    name of the request. It gives the options of each choice input whose
    dependencies have answers, cut at 1,000, with the `WaitingFor` reason of
    the others. It needs the action of the operation, as a submit does:
    `catalog:request` on the blueprint for a create, and `unit:edit-inputs`
    on the unit for an update. A form calls it after each answer.
12. **Dependents.** A revision whose answer comes from a source records a
    dependency edge from the source unit to the unit of the request
    (ADR 153), with the kind `output-read` and the evidence `declared`, when
    the revision is applied. The answer is a value of the source, so the unit
    of the request depends on it as on a read. So a delete of the network
    unit warns about the databases that chose it.

## Consequences

- The options of a form are always the options of the server. A client
  cannot add one, and a stale form fails at submit, not at apply.
- A platform team shares the value and the label of an output with each
  person who can request from its blueprint. It shares nothing else.
- A change of an output between the approval and the apply rejects the job.
  The requester submits again, and the approvers see the new answers.
- Each recheck reads the outputs again. A source with 10,000 units costs one
  read of 10,000 rows at each point (ADR 117 decision 8).

## Open questions

- Does a source need an operator other than equality, for example "in a
  list" or a prefix? The owner decided equality for version 1.
- Can a source read units of another namespace? Version 1 limits a source
  to the namespace of the blueprint.
- Does a form need a search over a source with more than 1,000 options?
  Version 1 returns the first 1,000 and a truncated mark, and the check of
  an answer uses every option.

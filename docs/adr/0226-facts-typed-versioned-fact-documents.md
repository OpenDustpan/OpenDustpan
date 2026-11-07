# 226. Facts: typed, versioned fact documents

Status: accepted · 2026-10-18. Part of ADR 222. Supersedes ADR 115 decision 13, the name of ADR 135 decision 6, and ADR 144 decision 14. Supersedes in part ADR 194 decisions 6 and 8, and ADR 201 decision 5. Accepts RFC 49. Decision 4 superseded in part by ADR 240. The `state.write` fact schema extended by ADR 241. Decision 7 extended by ADR 258

Changed by:

- ADR 240 changes decision 4: principal.kind takes the record kinds, no agent.
- ADR 241 extends decision 4: the state.write fact schema gets the import
  object.
- ADR 258 extends decision 7: a third built-in gate of wider scope.
- ADR 263 changes decision 7: a refresh has no plan phase, except a drift
  check.
- ADR 286 changes: the words catalog and promotion become self-service and
  failover.
- ADR 342 extends decision 4: state facts for three more writes.

## Context

The policy input of a job start holds the key paths of the edits and their
old and new values (ADR 213 decision 4). The input of a Catalog request
holds the usage of each quota rule (ADR 115 decision 13). A cost estimate
arrives after the plan (ADR 144 decision 5), and no decision runs after the
plan (ADR 144 decision 14). A change order reads the plan summary and the
policy reports (ADR 194 decisions 6 and 8). A profile reads typed context
facts (ADR 201 decision 5). Each input has its own shape.

The spike gave `change.planned` a fact schema with plan counts, a cost fact
with `available` and `deltaMonthly` in cents, the changed paths, the input
keys, and the collection and labels of the unit. Cedar has no open map and
no quantifier over a set.

## Decision

1. **A fact (owner).** A fact is a typed value that the server produces
   before a decision point. A fact never decides. Only a gate, an approval
   default, an ownership rule or a fixed control decides.
2. **One schema for each decision point.** Each decision point has a fact
   schema with a version. The version has the form
   `<decision point>/<major>.<minor>`, for example `change.planned/1.0`. The
   schema is a Cedar schema. The decision record names the schema version.
   The record also holds the ID and the version of each path set that the
   schema of the unit held. A new field
   is a new minor version. A removed or changed field is a new major
   version, and the server type-checks each stored gate against it before
   it uses it. An access schema holds the facts of an access limit (ADR
   223 decision 4).
3. **A missing fact is a typed value (owner).** Each fact always has a value
   of its type. A fact that a producer can fail to give belongs to a group
   with an `available` flag. With no cost estimate, `cost.available` is
   `false` and `cost.deltaMonthly` is 0. A gate that needs the fact names
   `available`. Every built-in gate and starter pack fails closed: it treats
   `available == false` as a match of its `deny` or approval outcome.

   The check at save refuses a `condition` or an `unless`, of a gate, an
   approval default or an ownership rule, that reads a field of a group
   with an `available` flag and does not read that flag, with
   `gate.ErrInvalid`.
4. **Facts of `change.planned`.** Version 1.0 holds these groups:

   | Group | Fields |
   | --- | --- |
   | `principal` | `kind` (`person`, `service` or `agent`), `credential` (`session`, `access-token`, `job-token` or `trust-token`) |
   | `source` | `kind` (`api`, `cli`, `forge`, `catalog`, `agent` or `schedule`), `ref`, `protected`, `commit`, `upstreamUnit` |
   | `unit` | `tool`, `collection`, `labels` as a set of `key=value` strings |
   | `operation` | `kind` (`apply`, `destroy`, `refresh` or `import`) |
   | `change` | `inputKeys`, `secretKeys`, `truncatedKeys`, `paths` as sets of strings |
   | `plan` | `available`, `add`, `change`, `replace`, `destroy`, `resourceTypes` |
   | `cost` | `available`, `deltaMonthly` and `totalMonthly` in cents, `currency` |
   | `budget` | `available`, `level` (`ok`, `warning` or `exceeded`), `percentUsed` |
   | `drift` | `available`, `resources`, `ageSeconds` |
   | `resourcePolicy` | `available`, `mandatoryFailed`, `advisoryFailed`, `unreadable` |
   | `externalChecks` | `passed`, `failed`, `pending`, `timedOut` as sets of check names |
   | `quota` | `available`, `maxPercentUsed`, `exceeded` as a set of rule names |
   | `dependents` | `available`, `count`, `units` as a set of unit references |
   | `paths` | `available`, `touches` and `only`, each a record with one boolean for each path set (decision 13) |
   | `document` | `truncated` as a set of fact paths (decision 14) |

   The credential kinds are an owner decision of 2026-10-18. A token that a
   trust rule issued (ADR 54) is `trust-token`, so a gate can tell CI with
   workload identity from a person's access token.

   `change.propose` holds the groups that exist before a plan: `principal`,
   `source`, `unit`, `operation`, `change`, `quota`, `paths` and `document`. `change.apply` holds
   the groups of `change.planned`, with the values of that moment. An agent
   is a subject of gates through `source.kind == "agent"` and
   `principal.kind == "agent"`.

   `state.write` (ADR 224 decision 16) holds `principal`, `source`, `unit`,
   `operation`, `plan` (always `available == false`), `document` and a group
   of its own, `state`: `available`, `resources` and `removed`. Its
   `operation.kind` adds `lock`, `rollback`, `force-write`, `state-import`,
   `rename`, `delete`, `unlock` and `unlocked-write` to the kinds above.
5. **Facts of a profile.** `profile.save` and `profile.open` hold the profile
   name, `access` (`read`, `open` or `write`), `refProtected`, `jobOpen`,
   `routingChange` and `importsAdded`. These are the typed context facts of
   ADR 201 decision 5, in a Cedar schema.
6. **Secret values never are facts.** A fact holds the key of a secret
   input and never its value. ADR 213 decision 4 stays for the record: the
   old and new values of a non-secret input are in the decision record, not
   in the fact document.
7. **Plan summary and policy reports.** The plan summary and the resource
   policy reports (ADR 194 decision 8) are facts. Two built-in gates at the
   installation scope keep the behavior of ADR 194 in `enforce` mode:
   `builtin/plan-readable` denies when `plan.available` is `false` for a
   change that applies, and `builtin/mandatory-resource-policy` denies when
   `resourcePolicy.mandatoryFailed > 0` or `resourcePolicy.unreadable > 0`.
   The prefix `builtin/` is reserved for gate IDs. An administrator of the
   installation can switch each to `evaluate` mode, with a fresh
   authentication and an audit entry.

   **Scope of the built-in gates (owner, 2026-10-18).** Each built-in gate
   is in `enforce` mode by default, and holds for each change with a plan
   phase: a job or a change order whose operation is `apply` or `destroy`
   and that has a plan phase. ADR 194 held the same checks for change
   orders only. So their scope widens from change orders to all jobs with a
   plan phase. A job with no plan phase, for example a `refresh`, and a
   write at `state.write` meet no built-in gate.
8. **Cost gates (owner).** A cost gate is an ordinary gate at
   `change.planned` on the `cost` and `budget` facts. A budget stays
   notify-only (ADR 144 decision 13 stays). `change.planned` waits for the
   estimate for at most 15 minutes. After that, `cost.available` is `false`.
9. **Source filter (owner).** The setting of ADR 135 decision 6 is now the
   **source filter**: `source.Filter`, at the route
   `.../namespaces/{namespace}/source-filter`. It stays a fixed control. Its
   `protectedBranches` (ADR 213 decision 2) give the fact
   `source.protected`.
10. **External checks (owner).** An external check is a producer of facts.
    At a stage (after `change.propose` or after the plan), the server sends a
    signed request to an external service, with the signature of ADR 114
    decision 8, and waits for a signed callback. The answer is `passed` or
    `failed`, with a message. A check with no answer before its timeout (1
    minute to 1 hour, default 10 minutes) is in `timedOut`. A gate decides
    what a failed or timed-out check does. This accepts RFC 49: a run task is
    an external check, never a job step.
11. **Quotas.** A quota reservation (`units` and `requests` rules of ADR 115)
    stays a fixed control. Its usage is the `quota` fact. An `expr` quota
    rule becomes a gate at `change.propose` over the `quota` fact.
12. **Producers.** Each group of facts has one producer port in `pkg/gate`,
    with a conformance suite. A producer that fails gives
    `available == false`, never a guessed value.
13. **Named path sets (owner, 2026-10-18).** A path set is a stored object
    of a scope (the installation, a namespace, a collection or a unit) with
    an ID and 1 to 64 glob patterns. The ID matches the gate ID grammar
    (ADR 224 decision 1). A narrower scope cannot save a path set with the
    ID of a path set of a wider scope that holds it (`gate.ErrInvalid`), so
    each name means one set for a unit. The server matches the globs in Go
    against `change.paths`. For each path set that holds for the unit:
    - `paths.touches["<set>"]` is `true` when at least one changed path
      matches a glob of the set.
    - `paths.only["<set>"]` is `true` when at least one path changed and
      each changed path matches a glob of the set.

    `paths.available` is `false` when the server does not know the changed
    paths, for example for a change with no commit. The fact schema of a
    scope holds one attribute for each path set that holds for it, so the
    check at save refuses a gate that names an unknown set. The removal of a path set that a stored gate, default or ownership rule reads fails with
    `gate.ErrInvalid` and names each reader. Cedar compares names, and Go
    matches the globs.

    Benchmark: GitHub rulesets and GitLab approval rules match file paths
    with globs outside the rule language. Cedar has no quantifier over a
    set, so a named set gives a gate a boolean that it can read.
14. **A large fact document.** A fact document holds at most 256 KiB. Above
    that, the server empties the largest sets of strings (`change.inputKeys`,
    `change.secretKeys`, `change.paths`, `plan.resourceTypes` and
    `dependents.units`), largest first, until the document fits. It keeps
    each count, and adds the path of each emptied set to
    `document.truncated`. Each built-in gate and starter pack that reads a
    set fails closed when the path of the set is in `document.truncated`.
    The path facts of decision 13 are computed before the truncation. A
    document that does not fit with each of these sets empty fails with
    `gate.ErrLimit`.

## Consequences

- A cost estimate can reach a decision for the first time: a gate at
  `change.planned` reads it.
- Cedar cannot say "every changed path is under `modules/`". A gate reads
  `paths.only["<set>"]` instead, and Go matches the globs of the set.

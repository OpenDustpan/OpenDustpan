# 165. Unmanaged-resource findings and the actions on them

Status: proposed · 2026-10-17

## Context

On 17 October 2026 the project owner listed the actions on a finding:
import into a unit, ignore with a reason, write code, delete, and choose
the owner of a resource that is managed twice.

ADR 163 classifies each resource key, and ADR 164 runs the scans. This ADR
decides the finding: its record, its states, its actions, the ignore rules,
the audit entries and what the console needs.

Facts:

- The server never deletes a cloud resource and never edits state (ADR 28
  decision 3, ADR 153 decision 14). The tool does it, and the server sees a
  new state item.
- A change to a unit goes through a job (ADR 100). A job has four
  operations: `plan`, `apply`, `destroy` and `refresh`. Write-back edits only
  the config files of a unit (ADR 101 decision 4).
- A deleted unit can come back in its recovery window (ADR 47, ADR 153).

## Decision

### The finding

1. **One finding for each key in a namespace.** A finding
   (`unmanaged.Finding`) holds the kind, the resource key, the cloud type,
   the region, the name and the tags from the last scan, the units of the
   namespace that hold the key (at most 20, the others counted), a flag when
   a unit of another namespace also holds the key, the evidence (`state` or
   `scan`), the status, the first and the last time seen, and the last scan.
   A finding is namespace data, so its writes carry the fence and a backup
   of the namespace holds it.
2. **A finding outlives its units.** A purge of a unit deletes its index
   entries, not the findings that name it. The finding keeps the unit ID.

### States

3. **Four statuses.** `open` waits for a person. `ignored` has a reason.
   `pending` waits for an action that started. `resolved` has exactly one
   managing unit now, or a complete scan no longer sees the resource.
4. **One table.** `unmanaged.Next` is the only place that decides a
   transition. A test checks each pair of status and event against the
   table below, written out apart from the code. A pair that the table does
   not hold fails with `ErrTransition`.

   | From | Event | To |
   | --- | --- | --- |
   | open | observe | open |
   | open | reclassify | open |
   | open | ignore | ignored |
   | open | act | pending |
   | open | resolve | resolved |
   | ignored | observe | ignored |
   | ignored | reclassify | open |
   | ignored | unignore | open |
   | ignored | ignore-expired | open |
   | ignored | resolve | resolved |
   | pending | observe | pending |
   | pending | reclassify | open |
   | pending | action-ended | open |
   | pending | resolve | resolved |
   | resolved | observe | open |
   | resolved | reclassify | open |
   | resolved | resolve | resolved |

   The rules that need a reason:
   - A **reclassify** opens the finding from any status. A new kind is a new
     question: an ignore for "left behind" does not cover "managed twice".
   - An **observe** of a resolved finding opens it again. The resource came
     back.
   - A **resolve** wins over an ignore and over a pending action. The
     question is gone.
5. **The status that the caller read.** An action names the status that the
   person saw. The store refuses the action when the status changed
   (`ErrStatusChanged`), so two people never act on one finding at the same
   time without a warning.

### Actions

6. **The catalog.** `unmanaged.ActionCatalog` holds the actions, the kinds that
   each one applies to, and the actions of each unit that it changes. Each
   action also needs `namespace:triage-unmanaged` (new) on the namespace.

   | Action | Kinds | What happens | Also needs |
   | --- | --- | --- | --- |
   | `import` | not in any unit, left behind | Starts a job on a target unit that brings the resource into its state. The finding is `pending` until the index shows the key in the target unit. | `unit:write` on the target unit |
   | `write-code` | not in any unit, left behind | Starts a job on a target unit that generates the code of the resource and delivers it as a review request. | `unit:write` on the target unit |
   | `delete` | not in any unit, left behind | Records the decision to delete, with the person. For a unit in its recovery window, it can also start the restore of the unit and a `destroy` job. The finding is `pending` until a complete scan no longer sees the resource. | `unit:restore` and `unit:write` on the deleted unit, for the restore |
   | `choose-owner` | managed twice | Records the unit that keeps the resource. Starts a job on each other unit that deletes the resource from its state and leaves the cloud resource. | `unit:write` on each unit that gives up the resource |
   | `ignore` | all | Sets the status `ignored` with a reason and an optional end. | nothing |

   `unmanaged.SpecOf` refuses an action that does not apply to the kind of
   the finding (`ErrActionKind`). A test fails for an action that starts a
   job and does not check `unit:write`.
7. **The server never deletes a cloud resource.** `delete` records a decision
   and, for a resource that is not in any unit, shows the command of the
   cloud that deletes it. A scan pool has read-only credentials (ADR 164), so
   a delete from the scan pool is not possible, and it must stay so. A
   delete through a tool run is an open question.
8. **Jobs.** `import`, `write-code` and `choose-owner` start jobs through
   `job.Service.Start` (ADR 100 decision 17) with the person as the actor.
   The job record names the finding. When the job fails or a person cancels
   it, the finding gets `action-ended` and opens again. When the job
   succeeds, the next state item updates the index, and the next state check
   or scan resolves the finding. These three actions need job operations
   that ADR 100 does not have yet (see "Requests to other designs"). Until
   they exist, the three actions are not offered. The console shows the
   commands of each tool that do the same work by hand:
   - Import: `tofu import` or an `import` block, and `pulumi import`.
   - Write code: `tofu plan -generate-config-out=...`, and `pulumi import`
     with code generation.
   - Choose an owner: `tofu state rm` or a `removed` block with
     `destroy = false`, and `pulumi state delete`.
9. **Ignore.** An ignore needs a reason of 1 to 500 characters of text and
   can have an end, at most 400 days ahead. An ignore with no end lasts until
   a person stops it. `unmanaged.CheckIgnore` holds the rule. When the end
   comes, the schedule of ADR 164 sets the finding `open` again.

### Ignore rules

10. **A rule ignores new findings.** An **ignore rule**
    (`unmanaged.IgnoreRule`) ignores each new finding that it matches, with
    its reason. It selects by provider (required), account scope, type (exact,
    or a prefix that ends with `*`), a tag key with an optional value, and
    kinds. A rule can have an end. A namespace has at most 500 rules. An
    example: every resource of the type `AWS::EC2::*` with the tag
    `aws:default`, because each account has a default network.
11. **A rule never ignores old findings by itself.** A new rule applies to
    findings that are new after it. A person ignores the open findings that
    it would match with one action each, or with a bulk action in the
    console. So a broad rule cannot hide findings in silence. A delete of a
    rule does not open the findings that it ignored.
12. **A finding names its rule.** The ignore of a finding names the rule that
    set it, so a person sees why the server ignores the finding.

### Ports

13. **`unmanaged.FindingStore`** holds findings and rules: `Record`,
    `ResolveUnseen`, `Get`, `List`, `Act`, `ExpireIgnores`, `PutRule`,
    `DeleteRule` and `Rules`. Each write carries the fence. An action of a
    person appends its audit entry in the same transaction. The writes of a
    scan append no entry for each finding: the end of the scan appends one
    entry with its counts. The suite in `unmanagedtest` states the
    behavior, and a reference adapter in memory runs it today.
14. **Bounds.** A page of findings is at most 500. One call to `Record` takes
    at most 1,000 observations.

### Actions of authorization and audit

15. **Actions:**

    | Operation | Action | Object |
    | --- | --- | --- |
    | List and read findings and rules | `namespace:read-unmanaged` (new) | namespace |
    | Ignore, stop an ignore, choose an owner, delete, import, write code | `namespace:triage-unmanaged` (new) | namespace |
    | Create or delete an ignore rule | `namespace:triage-unmanaged` (new) | namespace |
    | The jobs of an action | the actions of the job (ADR 103) | each unit that the job changes |

    A finding names only the units that the caller can read (`unit:read`), and
    counts the others. The `health` section of ADR 161 counts the open
    findings that name a unit, with `unit:read` on that unit only.
16. **Audit entries.** Each action appends one entry: the action, the finding
    ID, the resource key, the old and the new status, the reason of an
    ignore, the chosen unit of `choose-owner`, the target unit of `import` and
    `write-code`, and the job IDs. A create or a delete of a rule appends one
    entry with the selector and the reason.
17. **Routes.** Proposals for the route table:
    - `GET /api/unmanaged/{namespace}/findings?kind=&status=&unit=&provider=&scope=&type=&after=&limit=`
    - `GET /api/unmanaged/{namespace}/findings/{id}`
    - `POST /api/unmanaged/{namespace}/findings/{id}/actions` with the action,
      the status that the caller read, and the fields of the action
    - `GET` and `POST /api/unmanaged/{namespace}/rules`
    - `DELETE /api/unmanaged/{namespace}/rules/{id}`

### What the console needs

18. The console needs:
    - a page of findings for each namespace, grouped by kind, with filters by
      status, unit, provider, account scope and type, and counts of each,
    - on each finding: the evidence, the units, the mark "also in another
      namespace", the first and the last time seen, the last scan and the
      history of its actions from the audit log,
    - the actions of each kind, with the commands of each tool when the job
      operation does not exist,
    - a choice of owner that shows the two units side by side, with their
      owners and on-call (ADR 160) and their last change,
    - the ignore rules with the count of findings that each one ignored,
    - the scan targets and their scans, with the counts, the scopes that
      were not complete and their errors (ADR 164),
    - a warning on each scan target that the server cannot prove its
      credentials read-only,
    - on the page of a unit, the count of open findings that name it, and on
      the graph (ADR 162), a mark between two units that manage one
      resource.

## Consequences

- A team sees every unmanaged resource in one list, with one owner for each
  question.
- Findings that are left behind and managed twice work on the first day,
  with no runner. Actions that need jobs wait for the job operations. Until
  then, the console shows the commands.
- No finding closes because a scanner failed. Only a complete scope or a
  state that one unit manages closes a finding.
- An ignore always has a reason in the audit log.

## Requests to other designs

- **The job model (ADR 100, ADR 101).** Three job operations or kinds:
  - `import`: brings one cloud resource into the state of the unit of the
    job, with the import block of OpenTofu or `pulumi import`.
  - `forget`: deletes one resource from the state of the unit of the job and
    leaves the cloud resource, with a `removed` block or `tofu state rm`,
    and `pulumi state delete`.
  - code generation delivered by write-back: a write-back that adds a new
    file of code in the directory of the unit. ADR 101 decision 4 allows
    only config and variable files today.

  Each of them names one unit, so the rule "one job, one unit" holds.
- **The job record.** A job names the finding that started it, so the
  console links the two, and the end of the job updates the finding.

## Open questions

1. **Delete through a tool.** Should `delete` of a resource not in any unit
   start an `import` into a temporary unit and then a `destroy` job? It
   would delete through a tool, with a plan to read first, at the cost of two
   jobs.
2. **Bulk actions.** Is a bulk ignore one API call with a list of finding IDs,
   with a limit? RFC 44 proposes bulk operations.
3. **Notification.** Should a new managed-twice finding notify the owners of
   both units (RFC 23)?
4. **Retention.** How long does the server keep a resolved finding? The audit
   log keeps its actions. This ADR proposes 400 days, the retention of plans
   and logs in ADR 102.

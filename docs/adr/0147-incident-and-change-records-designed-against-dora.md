# 147. Incident records, change records and reviews, designed against DORA

Status: accepted · 2026-10-18 · the table in "Context", decision 3 and decision 6 and two open questions superseded in part by ADR 148

## Context

On 17 October 2026 the project owner decided that the project designs the
operations features against DORA where it applies: incident records, change
records and reviews. The owner also decided that the project never claims
compliance.

In this ADR, DORA is the Digital Operational Resilience Act, Regulation
(EU) 2022/2554. RFC 46 uses "DORA metrics" for the four delivery metrics
of the DevOps Research and Assessment program. The two are different. This
ADR uses the full name of the metrics where it means them.

We read these parts of the regulation as the ones that touch an
infrastructure backend. Each row still needs a check against the text of
the regulation. A research note must check each reference against the
text before a user document cites it (see "Open questions"):

| Part | What it asks, as we read it | Where OpenDustpan helps | Source |
| --- | --- | --- | --- |
| Article 9(4)(e) | documented policies, procedures and controls for ICT change management | change records: jobs, approvals, freezes, break-glass and its review | source check pending |
| Article 11 | response and recovery plans | on-call schedules, runbooks | source check pending |
| Article 13 | learning after incidents: reviews and root causes | the review after an incident | source check pending |
| Article 17 | a process to detect, log, classify and track ICT incidents | incident records | source check pending |
| Article 18 | classification of incidents by criteria, for example clients affected, duration, data losses and critical services | the classification of an incident by a person | source check pending |
| Article 19 | reports of major incidents to the authority: initial, intermediate and final | records of the reports that a person made | source check pending |

A review of this ADR gave these notes. They still need a check against
the text. The research note must check each one, and must not take it as
a fact:

- Article 9(4)(e): the review says that RTS (EU) 2024/1774 may also apply.
- Article 11: the review says that the row should say that OpenDustpan
  "supports" the plans, not that it "meets" the article.
- Article 13(2): the review says that it may ask for reviews only after
  major incidents, not after each incident.
- Article 17: the review says that the article may have a paragraph about
  root causes. Check which paragraph, and whether the row must name it.
- Article 18: the review says that RTS (EU) 2024/1772 may hold the
  criteria and the thresholds.
- Article 19: the review says that RTS (EU) 2025/301 and ITS (EU) 2025/302
  may hold the content, the time limits and the templates of the reports,
  and that Article 19(2) and 19(3) may need their own rows.
- Articles 28 to 30: the review says that these articles, on the risk of
  ICT third parties, may need rows or a note that they are out of scope,
  with ITS (EU) 2024/2956 for the register of information.
- Articles 8, 10 and 12: the review says that each one may need a row or
  a note that it is out of scope.

OpenDustpan is one tool of the many that a financial entity uses. It can
keep facts that the entity needs. It cannot make an entity compliant, and
the docs must never say that it does.

## Decision

### Incident records

1. **An incident is an object of a namespace.** `incident.Incident` has an
   ID with the prefix `inc-`, a title, a severity from 1 (highest) to 4, a
   state, the times of each step, the person who opened it and the
   commander, the on-call field that paged, and links to units, jobs and
   break-glass reviews. The time of detection can be before the time of
   the open, so a record keeps when the team first saw the problem.
2. **States.** `open`, `acknowledged`, `mitigated`, `resolved` and
   `closed`. `incident.Apply` holds the steps, and a table-driven test
   covers them. A resolved incident can open again. A closed incident is
   final for its steps and its fields. It still takes notes,
   classifications and reports (decisions 4 to 6), because a final report
   and a late classification can come after the close. A create stores an
   incident only in the state `open`: `Incident.ValidateNew` refuses every
   other state and every time of a later step. `Incident.Validate` refuses
   an unknown state, a state without the time of its step, a time of a
   later step than the state, and a closed incident without `ClosedAt`, a
   root cause and lessons. `Store.Step` runs `incident.Apply` on the
   stored incident in the transaction of the write, so a step always
   starts from the stored state. `Store.Update` keeps the stored ID,
   namespace, state, step times, `OpenedAt` and `OpenedBy`, so an edit
   cannot change the state or the person who opened the incident.
3. **The review after an incident.** An incident closes only with a root
   cause and lessons. This is designed against Article 13 (source check
   pending). The close is a step of a person with
   `namespace:respond-incidents`.
4. **A timeline.** Each incident has an append-only timeline of notes. A
   step, a link of a job and a change of the classification add a note by
   the server. A person adds notes. The timeline of a closed incident still
   takes notes.
5. **Classification by a person.** A person with
   `namespace:classify-incidents` records whether the incident is major,
   and the criteria that the person found met. The criteria are names from
   a list of the installation. The server checks no criterion and never
   decides by itself that an incident is major. The classifications are
   append-only: `Store.SetClassification` appends one, and the last one is
   the current classification. So a late reclassification keeps the
   earlier ones, and a closed incident takes it too.
6. **Reports outside OpenDustpan.** A person records each report that the
   entity sent to an authority: its kind (for example initial,
   intermediate or final), its reference and its time. The reports are
   append-only: `Store.AddReport` appends one, also on a closed incident,
   because a final report can come after the close. `Store.Update` never
   changes the classifications or the reports. The server never sends a
   report.
7. **Paging.** The open of an incident of severity 1 or 2 sends the
   notification `incident.opened` (package `notify`) with the severity
   `critical` to the person on call of
   the namespace (ADR 143), at once and with no quiet hours (ADR 145
   decision 18). An acknowledge from the console, the API or chat (ADR 146)
   stops the escalation of the page.
8. **Runbooks on the incident.** The incident page shows the runbook of
   each linked unit and its resources (ADR 143 decision 13), and the
   resolved owners and on-call of each one (ADR 161).

### Change records

9. **A job is the change record.** For a change that OpenDustpan starts,
   the job holds who started it and for whom (the agent), the plan, the
   approvals and their fresh authentication, the gates, the freeze and the
   break-glass, the result and the state version that closed it (ADR 100
   to ADR 103). A break-glass job also has its review (ADR 142). No new
   object is necessary.
10. **Changes outside OpenDustpan.** A team that applies from its own CI
    writes state with no job. The history of the unit records each state
    write with its actor: the principal, the access token and the job ID,
    which is empty for a write with no job (ADR 150 decision 3). The change
    record view shows both.
11. **One export.** `GET /api/namespaces/{ns}/change-records?from=&to=`
    answers, as JSON lines, one record for each job and each state write
    with no job: the time, the unit, the principal and the agent, the
    operation, the approvals, the freeze and the break-glass with the
    verdict of its review, and the linked incidents. It needs
    `namespace:read-audit`, because it is an extract of the audit trail.
12. **Change and incident link both ways.** A person links a job to an
    incident, for example the change that caused it or the change that
    fixed it. A link has a role: `cause`, `fix` or `related`. So the
    delivery metrics of RFC 46 (change failure rate, time to restore) can
    read the records later.

### Reviews

13. **Three reviews.** Each one has a person, a time and a record:
    - the approval of a job before its change (ADR 103),
    - the review of a break-glass after its use (ADR 142),
    - the review after an incident (decision 3).

### API, actions and audit

14. **Routes.**

    | Route | Action | Object |
    | --- | --- | --- |
    | `GET /api/namespaces/{ns}/incidents[/{id}]`, `.../notes` | `namespace:read` | namespace |
    | `POST /api/namespaces/{ns}/incidents` | `namespace:respond-incidents` | namespace |
    | `PUT /api/namespaces/{ns}/incidents/{id}` (title, severity, links, texts) | `namespace:respond-incidents` | namespace |
    | `POST /api/namespaces/{ns}/incidents/{id}/steps` | `namespace:respond-incidents` | namespace |
    | `POST /api/namespaces/{ns}/incidents/{id}/notes` | `namespace:respond-incidents` | namespace |
    | `POST /api/namespaces/{ns}/incidents/{id}/classifications`, `POST .../reports` | `namespace:classify-incidents` | namespace |
    | `GET /api/namespaces/{ns}/change-records` | `namespace:read-audit` | namespace |

    The classification and the report routes append, so they are `POST`.
    The built-in role `writer` and the roles above it get
    `namespace:respond-incidents`. The role `admin` gets
    `namespace:classify-incidents`.
15. **Audit entries.** Each write of a person on an incident writes one
    entry of the kind `change`, in the scope of the namespace, with the
    incident ID. A classification and a report hold their fields. A read of
    the change records writes one entry of the kind `audit-read`, as an
    export of the audit log does (ADR 60 decision 9).
16. **Ports with conformance first.** `incident.Store` gets the suite
    `pkg/incident/incidenttest`, with a reference adapter in memory. Each
    write carries the fence of the namespace of its incident, and the store
    checks it in the transaction. An incident is never in the server scope,
    so a zero fence is always refused. A create with a zero fence or the
    fence of another namespace fails with `ErrInvalid`. A write on a stored
    incident with such a fence fails with `ErrNotFound`, so the answer does
    not tell whether the incident exists in another namespace. A stale
    fence fails with `namespace.ErrFenced`. The suite runs each write with
    the three fences. The store also refuses a write whose audit entry
    names another namespace, with the error of a fence of another
    namespace, so a write cannot append to the audit log of another
    namespace.

### The actions of the operations ADRs

17. **New actions.** ADR 140 to ADR 147 add these 14 actions to the action
    list of the `identity` package:

    | Action | Object | ADR | Built-in roles |
    | --- | --- | --- | --- |
    | `namespace:manage-calendars` | namespace | 140 | admin |
    | `server:manage-calendars` | server | 140 | site and account administrators |
    | `namespace:review-break-glass` | namespace | 142 | admin |
    | `server:review-break-glass` | server | 142 | site and account administrators |
    | `namespace:manage-on-call` | namespace | 143 | maintainer, admin |
    | `namespace:override-on-call` | namespace | 143 | writer and above |
    | `namespace:read-cost` | namespace | 144 | viewer and above |
    | `namespace:manage-budgets` | namespace | 144 | admin |
    | `job:write-cost` | unit | 144 | none: only the job token of a job of the unit |
    | `namespace:manage-notifications` | namespace | 145 | maintainer, admin |
    | `server:manage-notifications` | server | 145 | site and account administrators |
    | `caller:manage-chat-links` | caller | 146 | every person, from a session only |
    | `namespace:respond-incidents` | namespace | 147 | writer and above |
    | `namespace:classify-incidents` | namespace | 147 | admin |

    `job:write-cost` is for the job token only: no built-in role gets it,
    and no person or access token of a person holds it.
    `caller:manage-chat-links` is session-only: an access token never
    allows it, because a person links a chat identity from a session of
    the console.

    No agent action exists in this list. An agent acts as its user (ADR
    100 decision 17), and the rules of each ADR refuse an agent where a
    person must act: a break-glass verdict, a chat approval.

## Consequences

- An entity that must keep incident records and change records can keep
  them next to the changes, and export them with one route each.
- The project never says that it, or an entity that uses it, complies
  with DORA. The docs say "designed against DORA" and name the article
  that a feature helps with. `scripts/docs-banned-words.py` refuses the
  phrases `DORA compliant` and `DORA compliance` in the files that
  `make docs-lint` reads.
- `GLOSSARY.md` needs the terms Incident, Commander, Classification,
  Change record and Review after an incident, and an entry that tells the
  regulation and the delivery metrics apart.

## Open questions

- (research) Check each article reference of the table in "Context" against
  the text of Regulation (EU) 2022/2554 and its technical standards. Write
  the result as a research note before a user document cites an article.
- Does an alert from a monitoring tool open an incident through a signed
  inbound webhook? This ADR has incidents that a person opens.
- Should the list of classification criteria come from the technical
  standards with a default list, or stay empty until an administrator
  fills it?
- Does a namespace need an incident number that people say, for example
  `SHOP-42`, next to the ID?

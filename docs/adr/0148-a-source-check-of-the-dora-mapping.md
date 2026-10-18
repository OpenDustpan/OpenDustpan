# 148. A source check of the DORA mapping of incident and change records

Status: accepted · 2026-10-18 · supersedes ADR 147 in part. Route paths and `server:` action names superseded in part by ADR 200

## Context

ADR 147 designs incident records, change records and reviews against DORA,
the Digital Operational Resilience Act, Regulation (EU) 2022/2554. Its table
of articles had the note "source check pending" on each row. Its first open
question asked for a check of each reference against the text.

The project owner asked for that check. We read the consolidated texts on
EUR-Lex:

| Short name | Text |
| --- | --- |
| DORA | Regulation (EU) 2022/2554 (CELEX 32022R2554) |
| RTS 2024/1774 | Commission Delegated Regulation (EU) 2024/1774, ICT risk management |
| RTS 2024/1772 | Commission Delegated Regulation (EU) 2024/1772, classification of ICT-related incidents and cyber threats |
| RTS 2025/301 | Commission Delegated Regulation (EU) 2025/301, content and time limits of incident reports |
| ITS 2025/302 | Commission Implementing Regulation (EU) 2025/302, templates of incident reports |

The check found these problems in ADR 147:

- Article 13 is the wrong reference for a review of each incident.
  Article 13(2) asks for a review after a major ICT-related incident only.
  It names four subjects that the review checks. The root cause of each
  incident comes from Article 17(2).
- Article 9(4)(e) is correct, but RTS 2024/1774 Article 17 gives the
  details. The records hold no fact for "tested" or for "verified", and no
  purpose, scope, expected outcome or fall-back of a change.
- The Article 11 row is too wide. Only some paragraphs touch runbooks, the
  timeline and on-call.
- Articles 17, 18 and 19 ask for more facts than the records hold: the time
  of occurrence, significant cyber threats, the criteria of Article 18(1)
  and of RTS 2024/1772, recurring incidents, more report kinds, client
  notices and notices to the senior management and the management body.
- Articles 6(5), 8, 9(4)(c) to (d), 11(10) and 12 need rows. Articles 10,
  24 to 27 and 28 to 30 need a note that they are out of scope.

OpenDustpan is one tool of the many that a financial entity uses. Its
features support the obligations of the entity. The server decides nothing
for the entity: a person classifies, a person reports, and a person reviews.
The project never claims compliance. The docs say "designed against DORA"
and name the article.

Some mappings are a fact: the text names a record, and the feature keeps
that record. Other mappings are our interpretation. The table says "may
support" for each interpretation. A user document keeps that hedge.

## Decision

### The mapping

1. **This table replaces the table of ADR 147.** A user document can cite a
   row of this table, with its hedge. It names the article and says
   "designed against DORA".

   | Part | What it asks, as we read the text | Where OpenDustpan helps |
   | --- | --- | --- |
   | DORA Article 6(5) | a review of the ICT risk management framework, also after a major ICT-related incident | may support: the review after a major incident (decision 9) gives facts to that review. That review stays outside OpenDustpan |
   | DORA Article 8(1), 8(3), 8(4) | identify and document ICT assets and their links and dependencies, and assess the risk at each major change | may support: the resource index (ADR 163) and the unit graph (ADR 162) document assets and dependencies. The risk assessment field of a change (decision 2) links an assessment |
   | DORA Article 9(4)(c), 9(4)(d) | limit access to what a role needs, and use strong authentication | may support: groups, roles, grants and the access review (ADR 52), and MFA (ADR 46). Also the life cycle of access tokens (ADR 53) and workload identity (ADR 54) |
   | DORA Article 9(4)(e) and RTS 2024/1774 Article 17(1)(a) to (g) (Article 38 for an entity under DORA Article 16) | documented ICT change management: record, test, assess, approve and implement each change, and check it after. Rules for emergency changes | supports (b) and (d) to (g), and may support (a) and (c): the job is the change record (ADR 147 decision 9). It gets the change fields (decision 2), the `verify` phase (decision 3) and independent approval (decision 4). Break-glass with its review is the emergency change (decision 5) |
   | RTS 2024/1774 Article 12(2)(c)(iii), 12(2)(d) | log changes, and protect the logs against change and unauthorized access | may support: the audit log with the audit chain (ADR 64), on by default |
   | DORA Article 11(2)(b), 11(2)(c), 11(3), 11(8) and RTS 2024/1774 Article 26(1)(d) | response and recovery plans, and a record of what the entity did in an incident | may support: runbooks on each unit (ADR 143 decision 13) and the timeline of each incident (ADR 147 decision 4) |
   | DORA Article 11(10) | an estimate of the yearly costs and losses of major ICT-related incidents | may support: the cost entries of an incident (decision 15) and their yearly sum. The cost port (ADR 144) gives the actual cloud spend as information only |
   | DORA Article 12 | backup policies, restore procedures and tests of them | may support: backups and restore (ADR 30, ADR 38, ADR 42) and a record of each restore test (decision 16) |
   | DORA Article 13(2) | a review "after a major ICT-related incident disrupts their core activities" that checks the response, the forensic analysis, the escalation and the communication | supports: the review after a major incident (decision 9) |
   | DORA Article 17(1), 17(2) | a process to detect, manage and notify ICT-related incidents, a record of each incident and each significant cyber threat, and the root cause | supports: incident records (ADR 147 decision 1), the root cause at each close (decision 7) and cyber threat records (decision 13) |
   | DORA Article 17(3)(c), 17(3)(d) and RTS 2024/1774 Article 23(1), 23(2)(c) | roles for incident response, and the plans to communicate and to escalate | may support: on-call schedules and paging (ADR 143, ADR 147 decision 7) |
   | DORA Article 17(3)(e) | a report of at least each major incident to the senior management and the management body | supports: the record of a notice to the senior management and the management body (decision 12) |
   | RTS 2024/1774 Article 22, RTS 2024/1774 Article 23(4), RTS 2024/1772 Article 3(1) and ITS 2025/302 fields 2.2 and 3.2 | the incident management policy: evidence kept safe for a limited period. The time of detection (field 2.2) and the time of occurrence (field 3.2) of an incident | may support: the retention period of incidents (decision 14) and the time of occurrence (decision 6). Article 23(4) may support the times, for anomalous activities |
   | DORA Article 18(1)(a) to (f) and RTS 2024/1772 Articles 1 to 9 | classify each incident by criteria, and find a major incident by thresholds | supports: the default criteria (decision 10), which follow RTS 2024/1772 Articles 1 to 7, and the record of the classification of a person. The server never decides |
   | DORA Article 19(1), 19(4), RTS 2025/301 and ITS 2025/302 | reports of a major incident to the authority, with their content and time limits | supports: records of the reports that a person made (decision 11). The server never sends a report |
   | DORA Article 19(2) | a voluntary notice of a significant cyber threat | supports: the notice records of a cyber threat (decision 13) |
   | DORA Article 19(3), first subparagraph, and second subparagraph | a notice to clients of a major incident that has an impact on the financial interests of clients. For a significant cyber threat, a notice to the clients that the threat may affect | supports: the record of a client notice of an incident (decision 12) and the client notice step of a cyber threat record (decision 13) |

   **Out of scope.** OpenDustpan does not help with these parts, and the
   docs do not name them as features:
   - DORA Article 10, detection. A monitoring tool detects. The link point
     is an inbound alert that opens an incident, which is an open question
     of ADR 147.
   - DORA Articles 24 to 27, tests of digital operational resilience and
     threat-led penetration tests.
   - DORA Articles 28 to 30, the risk of ICT third parties, the register of
     information and the terms of contracts. A self-hosted installation is
     software that the entity runs, so these articles are for the entity to
     apply. OpenDustpan Cloud (ADR 77) may be an ICT third-party service
     provider to a financial entity that uses it, so Article 30 may apply to
     those contracts.

### Change records

2. **Change fields.** A job gets four optional text fields: the purpose,
   the scope, the expected outcome and the fall-back plan (RTS 2024/1774
   Article 17(1)(d) and (e)). It also gets an optional risk assessment: a
   URL or a text (DORA Article 8(3)), and an optional planned window: a
   start time and an end time. The implementation timeline (RTS 2024/1774
   Article 17(1)(d)(ii)) is the planned window and the times that the job
   already holds: the time of its creation, and the start and the end of
   each phase. A start request gives them, and the
   job keeps them with its spec. A namespace setting
   `changeFieldsRequired` makes the four fields required for each job
   that changes. Then a start without them fails with `ErrInvalid`. The
   server checks only that a field has text. It never judges the text.
3. **A `verify` phase.** A new phase kind `verify` comes after the apply
   phase of a runner apply job and of a runner destroy job. It has no
   approval gate. It holds runner steps that change nothing, for
   example a smoke test of the team or a plan that must show no changes.
   The unit settings name its steps. With no steps, the job has no `verify`
   phase, and the change record has `verified: false`. A failed `verify` phase
   fails the job, but the change stays applied. The fall-back plan tells
   the person what to do next. A write-back job gets no `verify` phase in
   this ADR. Tests before the change are steps of the plan phase or the CI
   of the team.
4. **Independent approval.** ADR 103 lets an approval gate allow the
   starter to approve. The starter is also the implementer, because the job
   token is a token of the starter. A namespace setting
   `independentApproval` makes the starter never count for an approval
   gate, whatever the policy decision says (RTS 2024/1774 Article
   17(1)(b)). The change record of each approval says whether the approver
   is the starter. A state write with no job has no approval in
   OpenDustpan, and its change record says so.
5. **Emergency changes.** A break-glass job is the emergency change, and the
   review of its break-glass by a second person (ADR 142) is the review
   after it. The change record of a break-glass job has `emergency: true`
   and the verdict of the review.

### Incident records

6. **The time of occurrence.** An incident gets an optional time of
   occurrence, next to the time of detection (RTS 2024/1772 Article 3(1)).
   ITS 2025/302 asks for the time of detection in field 2.2 and for the
   time of occurrence in field 3.2. RTS 2024/1774 Article 23(4) may support the two times, for
   anomalous activities. The time of occurrence cannot be after the time of
   detection. This ordering rule is ours. An incident also gets an optional
   time of recovery of the services, which ITS 2025/302 asks for in the
   reports.
7. **A root cause at each close, with a category.** An incident still
   closes only with a root cause and lessons. We design this against DORA
   Article 17(2), not Article 13. The close also needs a root-cause category
   from a list of the installation. The list has three levels, as in the
   root-cause classification of ITS 2025/302. The default list copies the
   names of that template. An administrator can change the list.
8. **Links between incidents.** A person links an incident to another
   incident with the role `recurrence` or `related`. When a person
   classifies an incident, the console shows the incidents of the namespace
   of the last 6 months that have the same root-cause category. This is
   information only. RTS 2024/1772 Article 8(2) counts recurring incidents
   as one major incident in some cases. For a recurring incident, the
   specific apparent root cause counts, not the broad category (RTS
   2024/1772 Article 8(2)(b)). ITS 2025/302 field 4.2 may help. So the same category is a hint, and the console
   says so. The person decides whether the case applies.
9. **The review after a major incident.** When the current classification
   of a person says major, the close needs four more texts, one for each
   subject of DORA Article 13(2)(a) to (d): the promptness of the response,
   the forensic analysis, the escalation, and the internal and external
   communication. The server uses the classification of the person. It
   never decides that an incident is major. A closed incident can get a
   later classification as major. Then a person adds a review record with
   the same four texts. The review records are append-only, as reports
   are, and the close of the incident does not change.
10. **Default classification criteria.** The list of criteria of an
    installation starts with a default list. An administrator can change
    it. The default list holds the seven criteria of RTS 2024/1772
    Articles 1 to 7. DORA Article 18(1) is the context of these criteria.
    - the clients, financial counterparts and transactions affected
      (Article 1),
    - the reputational impact (Article 2),
    - the duration and the service downtime, as two values (Article 3),
    - the geographical spread (Article 4),
    - the data losses (Article 5),
    - the criticality of the services affected (Article 6). This is a
      condition, not a threshold of Article 9,
    - the economic impact (Article 7).

    A classification also records the field `maliciousAccess` (boolean): whether the
    person found that the threshold of RTS 2024/1772 Article 9(5)(b) is
    met. That threshold is met when "any successful, malicious and
    unauthorised access not covered by point (a) occurs to network and
    information systems, where such access may result in data losses". The
    console shows the rule of Article 8(1) as help text: "critical services
    affected (Article 6), and the threshold of Article 9(5)(b) met or two or
    more of the other thresholds of Article 9(1) to (6) met". The person
    decides. The server stores the time of the first classification as major.
    This answers an open question of ADR 147.
11. **Report records.** A report record has one of these kinds: `initial`,
    `intermediate`, `updated-intermediate`, `final` and `reclassification`
    (DORA Article 19(4), ITS 2025/302). It also holds the reference code of
    the authority and the reference code of the entity. The incident page
    shows the time limits of RTS 2025/301 Article 5(1) as information only:
    - the initial notice: 4 hours from the classification as major, and at
      most 24 hours from the time the entity knew of the incident,
    - the intermediate report: 72 hours from the initial notice,
    - the final report: 1 month from the last intermediate report.

    For a late classification, the initial notice is due 4 hours from the
    classification (RTS 2025/301 Article 5(2)). When a time
    limit ends on a weekend day or a bank holiday, the entity may submit by
    noon of the next working day (RTS 2025/301 Article 5(4)), with
    exceptions (Article 5(5) and 5(6)). The page cannot
    compute the bank holidays of each entity. So it labels each time that
    it shows "Article 5(1) only", and a person applies Article 5(2) and
    5(4) to 5(6).

    The page labels these times "information only". The server never
    marks a report as late, and never says that a report meets a time
    limit. The installation keeps the hours in a table that an
    administrator can change, because the texts can change.
12. **Notice records.** A person records a notice to clients (DORA Article
    19(3), first subparagraph) and a notice to the senior management and the
    management body (DORA Article 17(3)(e)).
    Each one has a time, the audience, the channel and a reference. The
    records are append-only, as reports are. The server never sends a
    notice.
13. **Cyber threat records.** A cyber threat record is a new object of a
    namespace, with an ID with the prefix `thr-`, a title, a description,
    the time of detection, links to units and notes. A person records
    whether the threat is significant, with a reason. A person also records
    each voluntary notice of the threat to an authority (DORA Article
    19(2)), and each notice of a significant threat to the clients that it
    may affect (DORA Article 19(3), second subparagraph). Each notice has an
    audience: an authority or clients. A person closes a threat record with a note on the outcome. The close is
    a step of a person with `namespace:respond-incidents`. The server never
    decides that a threat is significant. The
    package `incident` holds it, with the same fence and audit rules as an
    incident (ADR 147 decisions 15 and 16).
14. **Retention.** Incident records and cyber threat records stay for a
    retention period that the installation sets
    (`--incident-retention`). RTS 2024/1774 Article 22(d) asks for evidence
    that the entity keeps safe and for a limited period. The period counts from the close of the record.
    After the period, the server deletes a closed record and writes an audit entry. With no
    setting, the server deletes nothing. The audit chain (ADR 64) covers
    each write, and the read actions of ADR 147 decision 14 limit who reads.
15. **Cost entries.** A person adds cost entries to an incident: an amount,
    a currency, a kind (`direct` or `indirect`) and a note. They are
    append-only. A yearly export sums them for the incidents that a person
    classified as major (DORA Article 11(10)), one sum for each currency.
    The server never converts a currency. The cost port (ADR 144) gives
    the actual cloud spend in the time of the incident, as information only.
16. **Restore test records.** Each restore through the server (ADR 38)
    writes an audit entry with the backup ID and the result. A person can
    also record a restore test outside the server: the time, the backup ID,
    the result and a note. `GET /api/server/restore-tests` answers both.
    It needs `server:read-audit`.

### Routes and actions

17. **Routes.** These routes add to the table of ADR 147 decision 14. No new
    action is necessary.

    | Route | Action | Object |
    | --- | --- | --- |
    | `POST /api/namespaces/{ns}/incidents/{id}/notices` | `namespace:classify-incidents` | namespace |
    | `POST /api/namespaces/{ns}/incidents/{id}/costs` | `namespace:classify-incidents` | namespace |
    | `GET /api/namespaces/{ns}/incidents/costs?year=` | `namespace:read-audit` | namespace |
    | `GET /api/namespaces/{ns}/incidents/{id}/costs`, `.../notices` | `namespace:read-audit` | namespace |
    | `GET /api/namespaces/{ns}/cyber-threats[/{id}]`, `.../notes` | `namespace:read` | namespace |
    | `GET /api/namespaces/{ns}/cyber-threats/{id}/notices` | `namespace:read-audit` | namespace |
    | `POST /api/namespaces/{ns}/cyber-threats`, `PUT .../{id}`, `POST .../{id}/notes` | `namespace:respond-incidents` | namespace |
    | `POST /api/namespaces/{ns}/incidents/{id}/reviews` | `namespace:respond-incidents` | namespace |
    | `POST /api/namespaces/{ns}/cyber-threats/{id}/steps` | `namespace:respond-incidents` | namespace |
    | `POST /api/namespaces/{ns}/cyber-threats/{id}/classifications`, `POST .../notices` | `namespace:classify-incidents` | namespace |
    | `GET /api/server/restore-tests` | `server:read-audit` | server |
    | `POST /api/server/restore-tests` | `server:backup` | server |

    The reads of the cost entries and of the notices of an incident, and of
    the notices of a cyber threat, need `namespace:read-audit`, not
    `namespace:read`, because they are an extract of the audit trail. The other incident reads keep `namespace:read`.

    The change records export of ADR 147 decision 11 adds the change
    fields, the `verify` phase and its result, whether each approver is the
    starter, and `emergency`.

### What this ADR replaces in ADR 147

18. **Replaced parts.** This ADR replaces or adds to these parts of ADR
    147. The other parts stay.
    - The table and the notes of the review in "Context".
    - The reference to Article 13 in decision 3. Decision 7 above holds the
      reference.
    - The kinds of reports in decision 6. Decision 11 above holds them.
    - The first open question, on the source check, and the open question
      on the list of criteria. Decision 10 above answers it.
    - The banned phrases in "Consequences". Decision 19 below adds to them.
19. **Banned phrases.** `scripts/docs-banned-words.py` also refuses
    `complies with DORA`, `compliant with DORA`, `DORA-ready`,
    `meets Article`, `met Article`, `satisfies DORA`, `satisfies Article`,
    `fulfils DORA`, `fulfills DORA` and `DORA certified`, and their forms, in
    any case. A phrase that wraps across a line break counts too. Its
    self-test has a case for each one.

## Consequences

- A financial entity can keep more of the facts that the texts name next
  to the changes and the incidents. It still classifies, reports and
  reviews by itself.
- The job model gets a fourth phase kind, `verify`. Decision 3 extends the
  table of ADR 100 decision 4: a runner apply and a runner destroy get the
  phase when the unit settings name `verify` steps. Decision 4 extends the
  gate counting of ADR 103 decision 2 with `independentApproval`.
- The package `incident` gets cyber threat records, notices, costs, links
  between incidents and the root-cause categories. Each port change gets
  conformance tests first.
- `GLOSSARY.md` gets a section Operations with the terms Incident,
  Classification, Commander and Cyber threat record. The term Phase gets
  the kind `verify`. The ambiguity Retention gets the retention of
  incidents.
- The texts can change. A source check of this table is due when the
  Commission changes one of the five texts.

## Open questions

- Does a write-back job get a `verify` phase after the state version that
  closes it?
- What is a good default retention period for incident records? This ADR
  has none.
- Do the time limits of a report send a reminder notification, or stay
  only on the incident page?
- Does a cyber threat record need the criteria of significance of RTS
  2024/1772, as a default list?

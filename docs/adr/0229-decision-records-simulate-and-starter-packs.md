# 229. Decision records, the simulate route and starter packs

Status: accepted · 2026-10-18. Part of ADR 222. Supersedes ADR 114 decision 11, ADR 197 decision 6 for the "Policy decisions stored" entry. Accepts part of RFC 50

## Context

ADR 114 decision 11 stores for each engine and each point the effect, the
reasons and the SHA-256 of the input. A change order stores the combined
effect in an audit entry (ADR 197 decision 6). A person who sees a refused
change reads several records to learn why. RFC 50 asks for samples, a
simulator and the input shown with each decision.

The spike wrote one JSON record for each evaluation, also for an access
limit and for an error, and read it through one route.

## Decision

1. **One record for each evaluation (owner).** Each evaluation of a decision
   point for one change writes one decision record, with the ID prefix
   `gdr-`. It holds:
   - the decision point, the change (job, change order, Catalog revision or
     profile), the namespace and the time.
   - the fact schema version, the fact document and its SHA-256.
   - for each unit: each gate evaluated, with its version, mode, match and
     outcome, and each Cedar diagnostic.
      - the requirements, with the eligible approvers, the count and the
     signatures so far. A carried signature shows `carriedFrom` (ADR 225
     decision 12).
   - each gate passed by a break-glass, with the audit entry of the
     break-glass (ADR 224 decision 13).
   - the warnings and `autoApply` (decision 3).
   - the state of each fixed control (decision 4).
   - the result: `allow`, `warn`, `approval-wait`, `deny` or `error`.
2. **Every block is explained.** Each refusal, each approval wait and each
   warning of a change names the ID of its record. The API error, the
   console and the CLI link to it.
3. **A warn blocks auto-apply (owner).** `autoApply` is `true` only when the
   result is `allow`. A `warn` of a gate in `enforce` mode makes it `false`,
   and a person starts the apply. A `warn` of a gate in `evaluate` mode does
   not change it.
4. **Fixed controls in the record (owner).** The record shows the state of
   each fixed control that the server checked for the change: the workspace
      lock, a manual lock, an active freeze and its break-glass, the override
   switches, the agent mode, the confirmation step, the secret consent, the
   quota reservation and the quota counts, the safe delete check of ADR 153
   decision 5, and the plan staleness check with the maximum plan age (ADR
   102, ADR 191 decision 7). A fixed control does not change the combining of ADR 224
   decision 14. It refuses on its own, as its ADR says.
5. **Storage.** A record is namespace data. The store checks the fence and
   writes it in the same transaction as the step that it explains. A record
   lives as long as the job or the change order that it explains (ADR 198).
   A backup holds the records (ADR 42). The audit entry of the step names the
   record ID.
6. **The simulate route (owner).** `POST .../namespaces/{namespace}/gates/simulate`
   takes draft gates, draft approval rules and a filter of stored records:
      a decision point, a time range and at most 1,000 records. It replays
   only the records whose change the caller can read, and skips the others
   with no trace of them in the answer. It evaluates the drafts against the
   stored fact documents, and gives for each record
   the stored result and the new result. It stores nothing and changes no
   change. A draft rule is replayed through this route before `enforce`.
7. **Starter packs (owner).** A starter pack is a set of gates and approval
   rules that the server ships. A pack is "designed against" a named control
   framework, for example the change-management controls of ISO/IEC 27001
   Annex A or SOC 2 CC8.1. It never claims compliance. A pack installs in
   `evaluate` mode. Each gate of a pack has a test file that
   `gate.RunTestFile` runs in CI.
8. **Offline tests.** A test file holds gates and cases of facts with their
   expected results. `dustpan gates test <file>` runs it with no server.

Benchmark: Open Policy Agent decision logs hold the input, the policy
version and the result for each decision. AWS Verified Permissions gives a
test bench that runs a policy set against a request with no effect.

## Consequences

- A person can answer "why was this blocked" from one record.
- The record can hold large fact documents. Version 1 limits a fact
  document to 256 KiB. Above that, the server empties the largest sets and
  names them in `document.truncated` (ADR 226 decision 14).

---
rfc: 81
title: Open questions of the import tool and the move check
status: accepted
created: 2026-10-19
---

# RFC 81. Open questions of the import tool and the move check

Status: accepted. The product manager decided each question on 2026-10-19
for the owner, as the recommendation gives it. ADRs 241 to 244 record the
decisions.

## Problem

ADR 241 makes an import a resource of a namespace that the server runs.
ADR 242 says what it moves for each tool. ADR 243 gives the source port
and the first adapters. ADR 244 gives the move check, the guide pages and
the e2e steps. Some choices touch decisions of the owner (the gate model of
ADR 224, the quotas of ADR 115, the hosted service) or the scope of the
feature. They are below.

## 1. Which gate does an import meet?

ADR 224 decision 2 gives `state.write` for "a write of state that no job of
OpenDustpan makes". An import writes state with no job. One import unit can
write hundreds of versions.

- **A. `state.write` once for each import unit,** before its first write,
  with the facts of the import (the source kind, the count of versions, the
  bytes, the target unit). A `deny` skips that unit. A requirement holds the
  unit in `planned` until it is met. The fact schema of `state.write` gets
  an optional `import` object (ADR 226).
- **B. `state.write` for each version.** The same rule as a CLI write. It
  evaluates the gates hundreds of times for one decision, and an approval
  for each version makes no sense.
- **C. A new decision point `import.run`.** A new decision point is a new
  ADR (ADR 224 decision 2). Policies that guard state writes must then
  name two points.

**Recommendation: A.** One decision for one unit, on the point that already
guards writes with no job. The routes of ADR 241 decision 21 do not ship
before this question has an answer.

## 2. Does an import count against quotas?

ADR 115 gives quotas and reservations. An import can add many units and
many bytes at once, also on OpenDustpan Cloud.

- **A. Yes. The dry run reserves.** The dry run reports the units and the
  bytes against each quota. The `run` reserves them, and fails before the
  first write when a quota is short.
- **B. Yes, at each write.** An import can stop in the middle on a quota.
- **C. No.** An import is outside quotas.

**Recommendation: A.** A team sees the cost before the move, and an import
never stops half way on a quota.

## 3. Engine events, run logs and plans of the source

ADR 242 moves state and the metadata of each version. It does not move the
engine events of a Pulumi update, or the runs, plans and logs of a
TFE-compatible workspace.

- **A. Not in the first version.** The plan lists them as "not moved".
- **B. An option `events: true` for the Pulumi Cloud-compatible source.**
  The API of the source answers the events of each update. The import
  seals them like other event payloads (ADR 151). Blame and the history
  view get the full events. It costs storage and one request for each
  update.
- **C. Also the run logs of a TFE-compatible source.** A run log is not
  part of a state version, and OpenDustpan has no TFE run model for
  OpenTofu with local execution (ADR 13).

**Recommendation: A now, then B.** The state and the version metadata are
what a team needs to switch. B can come with its own e2e step. C does not
fit the model.

## 4. Source users and teams

ADR 242 decision 3 keeps the user of a source version as text. ADR 244
decision 6 reports a missing grant as `carry.access`. RFC 42 asked for
access "mapped to roles as a proposal that an admin accepts". RFC 66 asks
if the tooling maps a source principal to a target principal.

- **A. Report only.** No mapping.
- **B. A proposal.** The dry run reads the teams and the access of the
  source, where the source API gives them, and proposes groups and grants.
  An administrator accepts each one. The import never applies a grant by
  itself.
- **C. Map automatically.** It applies access that nobody in OpenDustpan
  reviewed.

**Recommendation: A in the first version, B later.** B needs a read of
the access model of each source and a review screen. A keeps the risk at
zero: no access exists that a person did not give.

## 5. Re-encrypt passphrase and KMS secrets on the server

ADR 242 decision 10 keeps a passphrase or cloud KMS secrets provider as it
is, and gives the command that changes it.

- **A. Keep the provider.** The team runs
  `pulumi stack change-secrets-provider default` after the move.
- **B. The server takes the passphrase, or a KMS credential, as an
  ephemeral credential and re-encrypts.** The server then needs the crypto
  of each provider of the Pulumi CLI, which this project does not own.

**Recommendation: A.** One CLI command does the change with the CLI's own
code. B adds code that must follow another project's crypto.

## 6. A source adapter on the workstation

ADR 241 decision 3 gives the upload source for a source that the server
cannot reach. The `opendustpan import upload` command reads with the
tool's CLI, so it moves only what the CLI reads (the current state, or each
`pulumi stack export --version`).

- **A. Upload only.** One server-side path, one upload path.
- **B. The CLI also runs the source adapters of ADR 243 on the
  workstation**, and sends each version to the upload route. A private TFE
  server or a private bucket then imports with its full history, with the
  credential only on the workstation. It doubles the places where an
  adapter runs.

**Recommendation: A now, B when a team asks for it.**

## 7. Retention of imports and ledgers

An import record, its plan and its ledger rows tell where each history
item came from.

- **A. Keep the ledger rows with the unit, and the import record for 400
  days after its release or its cancel.** A purge of the unit (ADR 47)
  deletes its ledger rows. The audit entries of the import stay under the
  retention of the audit log (ADR 68).
- **B. Keep everything with the namespace.**
- **C. Delete at the release.**

**Recommendation: A.** The provenance of each item stays as long as the
item. 400 days is more than the 12 months that PCI DSS v4.0.1 requirement
10.5.1 asks for an audit history, and the audit entries keep the facts of
the import after that.

## 8. Product names in the console

ADR 243 decision 9 names a source kind by its protocol in the API, and
names products only as "works with" text.

- **A. "Works with" text in the docs and the console, no logo.**
- **B. Protocol names only, also in the docs.** A person may not know that
  `pulumi-service` is the kind for Pulumi Cloud.

**Recommendation: A.** It follows CLAUDE.md: "works with the Pulumi CLI",
"a TFE-compatible API", and never another project's name, logo or colors
as ours.

## Outcome

The product manager decided each question on 2026-10-19, with a benchmark
for each decision in the ADR.

1. **A.** `state.write` once for each import unit, with the
   `operation.kind` value `unit-import`. A `resume` while `holding` that
   moves new versions evaluates the gates again. The plan-stale rule does
   not apply while a unit waits on a requirement. The `import` fact object
   is a new minor version of the fact schema of `state.write` (ADR 241
   decision 23).
2. **A.** The dry run reports, and the run reserves. A `resume` while
   `holding` reserves again, the upload source reserves at each upload, a
   cancel frees and a release settles (ADR 241 decision 24).
3. **A.** No engine events, run logs or plans now (ADR 242 decision 16).
4. **A.** Report only (ADR 244 decision 16).
5. **A.** No server-side re-encryption of passphrase or KMS secrets (ADR
   242 decision 10).
6. **A.** Upload only for workstation sources (ADR 243 decision 13).
7. **A.** The ledger rows stay with the unit, and the import record stays
   for 400 days. The purge keeps the ledger rows and the `importID` of each
   history item (ADR 241 decision 25).
8. **A.** "Works with" text only, no logo (ADR 243 decision 9).

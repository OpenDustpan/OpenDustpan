## Context

Owner decision 7 (2026-11-20) lets a non-person principal approve a job approval under four conditions (see the proposal). RFC 134 records the decision, the exact ADR words that change, and the options. This design implements the recommended option (a separate list `machineIds`) and keeps each open question of the proposal as a named seam, so that the owner's answer changes one task and not the structure.

This change starts after `several-approval-requirements-one-change` (the matching, `Gate.Name`, `Approval.For`, users by ID and the fresh window of each gate are on main) and after the ADR that follows RFC 134 is accepted.

The flow on main, which this change keeps: `POST .../jobs/{job_id}/approvals` calls `Approve` (`pkg/job/service_approve.go:38`), which reads the waiting gates, checks the approver (`checkApprover`, `:208`) and records the verdict (`record`, `:347`). The reconciler dispatches when `GatesStatus` is open.

## Decisions

1. **Who is a machine approver.** A request whose credential is an access token (`CredentialAccessToken`; an empty credential counts as one, `pkg/identity/identity.go:75`) and whose kind is `KindService` or `KindPerson`. A console session of a person is not a machine approver. A job token and a trust token are not machine approvers, and stay refused (`ActsAsPerson` is false for both, `pkg/identity/identity.go:137`). `ActsAsPerson` is true for the access token of a person, so it is not the test of this rule. The test is the credential and the kind, in one function of `pkg/job` that returns `approverPerson`, `approverMachine` or `approverRefused`, and refuses an unknown kind (fail closed).
2. **The rule names it by stable ID (condition 1).** `Approvers` gets `MachineIDs` (prefix `prn-`). The config service checks at save that each ID is a stored principal of kind `service` or `person`, and refuses a name with `gate.ErrInvalid`. An empty or unknown ID counts no one. A principal that someone removes and makes again has a new ID (ADR 362 decision 1). The field is a separate list, so `groups`, `users` and `unitOwners` can never admit a non-person: a class of bug that the type makes impossible. This is open question 5.
3. **A person in a session is not changed.** An entry in `users` or in a group admits a person in a session and never the access token of that person. The access token of a person approves only when the ID of the person is in `MachineIDs`.
4. **Self-approval (condition 2).** A machine approval is refused when `Approval.Subject` equals `StartedBy`, also when a gate sets `AllowSelf`. `AllowSelf` and `authorMayApprove` never apply to a machine. The same rule is in `GatesStatus`, so a stored approval that breaks it fills no slot. The machine rule must not be folded into the `AllowSelf` check at `pkg/job/service_approve.go:225`: it is its own check. The author of an uploaded plan is read from the record that the upload change adds (ADR 378 decision 4). Until then the machine rule has nothing to read, and the task for it waits. A token created by the starter (`Token.CreatedBy`, `pkg/accesstoken/accesstoken.go:159`) is open question 1, option A.
5. **Fresh window (condition 3).** A requirement that stores `freshAuthSeconds` is out of reach for a machine, whatever the effective value of `freshAuthEnabled` is (reading A of open question 2). For `freshAuthForApprovals` and for a `destroy` operation, the effective value of `freshAuthEnabled` decides (ADR 385 decisions 1 and 2): with the setting on, a machine cannot approve, because it cannot give the step; with the setting off, a named machine may approve a `destroy`. In the terms of `several-approval-requirements-one-change`: `For` of a machine approval holds only the gates with no stored window. When no gate is left, the approval is refused with the cause "no fresh step", as task 5.4 refuses a stale step.
6. **Marks (condition 4).** `approvalEntry` (`pkg/job/service_approve.go:391`) writes the change `machine` with the value `true`, and the change `credential` with the value `access-token`, and the entries that exist (`Entry.TokenID`, `PrincipalKind`, `pkg/audit/audit.go:437`). `PrincipalKind` alone does not tell a token of a person from a session, so the change `machine` does. `Approval` (`pkg/job/gate.go:172`) gets `Machine bool` and `Credential`, and the codec keeps them. `Signature` (`pkg/gate/record.go:91`) gets `Machine bool` and `TokenID`, and names the entry of `machineIds` that admitted the principal. The server sets all of them. A refusal of a machine approval writes an audit entry of the outcome `denied` with the cause.
7. **The count.** A machine approval fills one slot of one requirement, as a person does, and one subject fills one slot (ADR 225 decision 6). Whether two named machines may fill a count of 2 is open question 3.
8. **Scope.** Only `job:approve`. A change order and a profile draft keep ADR 225 decision 7. Rejection stays open to every holder of `job:approve` (`pkg/job/service_approve.go:251`).
9. **No SQL change.** The state is in the JSON body of the job row (`pkg/store/internal/jobstore/body.go`). `gateBody` gets `machineIds`, and `approvalBody` gets `machine` and `credential`. The rule field is stored with the rule body. No baseline changes and no migration is added.

## Contract

```go
// pkg/gate/rules.go (NEW field)
type Approvers struct {
    // ... fields of main ...
    // MachineIDs holds the stable principal IDs of the service principals
    // and the persons that may approve with an access token.
    MachineIDs []string
}

// pkg/job/gate.go (NEW fields)
type Gate struct {
    // ... fields of main and of several-approval-requirements-one-change ...
    MachineIDs []string
}

type Approval struct {
    // ... fields of main and of several-approval-requirements-one-change ...
    // Machine is true when a non-person principal approved. The server sets it.
    Machine bool
    // Credential is the credential kind of the approval. The server sets it.
    Credential string
}
```

Sentinels: `ErrAgentDenied`, `identity.ErrDenied` (as `*DeniedError` on `job:approve`), `ErrSelfApproval` and `gate.ErrInvalid` as on main. A refusal for a window uses the code `mfa.fresh-step-required` (task 5.4 of the other change).

Endpoints: no new route. The approval rule and approval default routes get `approvers.machineIds`. `GET .../jobs/{job_id}` gets `gates[].machineIds` and `approvals[].machine`. The request of `POST .../approvals` does not change, and a client value of `machine` or `for` is ignored.

## Layers (defense in depth)

Authorization per route is unchanged and stays: `POST .../jobs/{job_id}/approvals` reads `job:read` and writes `job:approve` on the unit of the job (`internal/api/jobs/routes.go:157`). The layers of a job approval are the rule save, the store codec, the service, the matcher and the HTTP handler. Each layer stops the attack alone.

| Layer | Guard | Hostile test (fails when the guard is removed) |
| --- | --- | --- |
| Rule save (`pkg/gate` config service) | Each `machineIds` entry is a stored principal ID of kind `service` or `person`. A name, an empty value or an unknown ID is `gate.ErrInvalid`. | A table: a name, an empty ID, an unknown ID, a group ID, a workload ID. |
| Store codec (`jobstore`) | `machine` and `credential` round-trip. A stored approval with `machine` true and a gate with a window fills no slot. | The job store conformance suite on both dialects; a restored-backup row. |
| Service (`Approve`) | The credential and kind test, the `machineIds` check, the starter refusal that ignores `AllowSelf`, the window rule. | The rows of the table "Hostile cases" below. |
| Matcher (`pkg/job/match.go`) | An approval with `Machine` true fills a slot only on a gate that holds the subject in `MachineIDs` and has no stored window, and never for the starter. | A table in `pkg/job/match_test.go`. |
| HTTP (`internal/api/jobs`) | The route keeps `job:read` and `job:approve`. A client value of `machine` or `for` is ignored. | `internal/servertest/jobs_api_test.go`: a body with `machine: false`. |
| Fail closed | An unknown kind or credential, a read error of the principal store, or a missing reader denies and writes an audit entry. | A row for each. |

Hostile cases (each is a row in a table test, and each fails when its guard is removed):

| # | Case | Expected | Layer |
| --- | --- | --- | --- |
| 1 | A service principal holds `job:approve` and is in no rule | refused; audited | service, handler |
| 2 | A service principal is a member of a group that the rule names | refused; a group never admits a machine | service, matcher |
| 3 | The rule names a person in `users`, and the person approves with an access token | refused; only `machineIds` admits a token | service, matcher |
| 4 | The rule names a service principal by name, not by ID | refused at save (`gate.ErrInvalid`) | rule save |
| 5 | The service principal is removed and made again with the same name | the new principal fills no slot; the rule holds the old ID | service, store |
| 6 | A machine starts a job and then approves it, with `AllowSelf` true | refused; `AllowSelf` does not apply | service, matcher |
| 7 | A person starts a job, makes a token for a named service principal, and the principal approves | refused when open question 1 is answered with option A | service |
| 8 | The requirement stores `freshAuthSeconds` 300 and `freshAuthEnabled` is off | a machine fills no slot (reading A of open question 2) | service, matcher |
| 9 | A `destroy` approval, `freshAuthEnabled` on, a named machine | refused with `mfa.fresh-step-required` | service, handler |
| 10 | A `destroy` approval, `freshAuthEnabled` off, a named machine, no window stored | accepted; the entry marks `machine` | service |
| 11 | The namespace sets `freshAuthForApprovals`, `freshAuthEnabled` on, a named machine | refused | service |
| 12 | Two gates: A has a window, B has none; a named machine approves | `For` holds B only; A stays open | service, matcher |
| 13 | A job token approves | refused; the job never approves its own change | service, handler |
| 14 | A trust token of a workload approves | refused | service |
| 15 | An agent acts with the access token of a named person | refused with `ErrAgentDenied` | service |
| 16 | A stored approval with `Machine` true and a gate with a window, from a restored backup | fills no slot | matcher, store |
| 17 | A client sends `machine: false` or a `For` list in the body of the approval | ignored; the server sets both | handler |
| 18 | The machine approves twice | the second call is `job.already-approved`; one slot | service |
| 19 | `count` is 2 and two named machines approve | allowed (open question 3, option A) | matcher |
| 20 | A rejection by a service principal that no rule names | the job is rejected (as on main) | service |
| 21 | The audit write fails | no verdict is stored | store |
| 22 | A machine approves a change order or a profile draft | refused (ADR 225 decision 7 stays) | service |

## Failure modes

- An administrator removes the ID from the rule after the machine approved: the approval counts, as of the approval time (owner decision of 2026-11-20 in the other change). The audit entry shows the token.
- An operator turns `freshAuthEnabled` on after a machine approved a `destroy`: the stored approval stays. A new approval needs a person.
- A token is stolen: it approves as the named principal. The owner accepted this risk for a named principal in decision 7. A rule can reduce it with a count of 2 and a second approver that is a person (open question 3). The owner can end the path at once: remove the ID from the rule, or revoke the token.

## Risks

- Four-eyes holds only as far as the token holders are different people. Open question 1 is the largest gap.
- Tasks 5.4 of the other change and 2.4 here touch the same lines of `checkApproval`. The order is fixed: 5.4 first.

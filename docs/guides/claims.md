# Apply claims

This guide shows how two runner jobs register disjoint apply claims on one
workspace before the apply, and how to list the claims. An apply claim is
the reservation of the resource instances that one saved plan changes and
reads. The server accepts a second claim only when its address set shares
no address with each accepted claim.

The examples use the host `dustpan.example.com` and these variables:

| Variable | Value |
| --- | --- |
| `$TOKEN` | The credential of the caller. A runner job sends its job token. The local wrapper sends an access token. |
| `$RELEASE` | The release of the caller, for example `0.4.0`. The server refuses a release that is newer than its own with the status 426. |
| `$WS` | The stable ID of the workspace: `data.id` in the answer of `GET /api/v2/organizations/{namespace}/workspaces/{name}`. |
| `$BASE` | The state version that the plan used: `data.id` in the answer of `GET /api/v2/workspaces/$WS/current-state-version`. |
| `$JOB` | The ID of the job that registers the claim. |

## Register a claim

A runner job registers its non-replace claim with its job token, and names
its own job in `jobID`. The server refuses a request with no job ID, with the
job ID of another job, or with the token of a job whose operation ended. A
token of another workspace answers 404, as a workspace that does not exist.
The server checks the credential before it reads the workspace, so a request
with no valid credential answers 401 whether the workspace exists or not.

| Route | Action | Object |
| --- | --- | --- |
| `POST /runner/v1/workspaces/{id}/claims` | `unit:claim-register` | The unit |
| `GET /runner/v1/workspaces/{id}/claims` | `unit:read` | The unit |

```sh
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Dustpan-Release: $RELEASE" \
  https://dustpan.example.com/runner/v1/workspaces/$WS/claims \
  -d '{"base":"'"$BASE"'","addresses":["aws_instance.a"],"jobID":"'"$JOB"'"}'
```

The answer holds the claim with state accepted, start time and principal:

```json
{
  "id": "clm-a1b2c3d4e5f6g7h8i9j0",
  "workspaceID": "ws-a1b2c3d4e5f6g7h8i9j0",
  "base": "sv-a1b2c3d4e5f6g7h8i9j0",
  "addresses": ["aws_instance.a"],
  "principal": "ada",
  "jobID": "job-a1b2c3d4e5f6g7h8i9j0",
  "state": "accepted",
  "startedAt": "2026-11-14T12:00:00Z"
}
```

A second job registers its disjoint claim through the same route. The
local wrapper registers with an access token and no job ID, and acts as
the person who signed in. It sends the access token in the header
`Authorization: token $TOKEN`, not `Authorization: Bearer $TOKEN`: the
server reads a bearer secret as a job token. The same job with the same base and the same
set gets the same claim ID with no second record.

## List claims

```sh
curl -s -H "Authorization: Bearer $TOKEN" \
  -H "Dustpan-Release: $RELEASE" \
  https://dustpan.example.com/runner/v1/workspaces/$WS/claims
```

The answer holds each claim with its state, base, address set, principal,
job ID, start time and end time, oldest first:

```json
{
  "claims": [
    {
      "id": "clm-a1b2c3d4e5f6g7h8i9j0",
      "workspaceID": "ws-a1b2c3d4e5f6g7h8i9j0",
      "base": "sv-a1b2c3d4e5f6g7h8i9j0",
      "addresses": ["aws_instance.a"],
      "principal": "ada",
      "jobID": "job-a1b2c3d4e5f6g7h8i9j0",
      "state": "accepted",
      "startedAt": "2026-11-14T12:00:00Z"
    }
  ]
}
```

## Refusals

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Claim base is touched by a later version on an address of the claim. | Server answers 422 with the touching version and names the current state version. Plan again from the current state version, then register a new claim. | `tofu plan` |
| Claim overlaps an accepted claim. | Server answers 409 with the blocking claim ID and stores no claim. Wait for the blocking claim to end, plan again, then register a new claim. | `GET /runner/v1/workspaces/{id}/claims` |
| Claim holds a replace action. | Server answers 409 with the code `claim.closure-unproven` and stores no claim. The closure test is not built yet, and a closure that the server cannot prove is a conflict. Wait for the accepted claims to end, then register again. | `GET /runner/v1/workspaces/{id}/claims` |
| Principal lacks unit:claim-register on the workspace. | Server answers 403 and names the missing action on the workspace. Ask a namespace administrator for the grant, then register again. | `GET /api/server/principals/{}/grants` |
| Claim holds an empty resource set. | Server answers 422 and changes nothing. Plan with at least one non-replace resource change, then register again. | `tofu plan` |
| Claim holds more than 10,000 addresses or an address longer than 1,024 bytes. | Server answers 422 with the bound and stores no claim. Send a smaller address set, then register again. | `tofu plan` |
| Claim names a missing, deleted or locked workspace or an unknown base. | Server answers 404 for a missing workspace, 410 for a deleted workspace and 422 for a locked workspace or an unknown base version. Use the current workspace and base, then register again. | `GET /runner/v1/workspaces/{id}/claims` |

Each accepted claim writes one audit entry, in the same transaction that
stores the claim. The server keeps the claims in its database, so a restart
does not lose them. A claim of a runner job ends, with an end time, in the
same transaction that moves its job to a terminal state, whichever path ends
the job, and each end writes an audit entry by the server with the job ID. A
claim of the local wrapper has no job, so no job end ends it: it ends only by
lease expiry, which writes an audit entry too. The server does not call the
expiry on a schedule yet, so a claim of the local wrapper stays accepted. A backup does not hold claims, and a restore leaves them
empty.

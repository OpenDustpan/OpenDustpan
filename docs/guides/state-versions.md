# State versions

This guide shows how the server records the state of a stack and of a
workspace, how it marks the current version and the final version, and how to
read and to restore a version.

The server keeps one state version for each state write of a unit. A unit is a
Pulumi stack or an OpenTofu workspace. Each state write sets the version in the
same fenced transaction as the state itself: the server writes the state and
the version together, or neither. The write needs no extra flag from the
client.

The server marks two versions of a unit:

- the **current version**: the version of the last state write. A plan and an
  apply read it.
- the **final version**: the version that ends a run. A job that waits for the
  state of a run reads it.

## The current version

Each state write sets the current version:

- A Pulumi update writes a checkpoint. The checkpoint of the update becomes the
  current version of the stack.
- An OpenTofu run writes a state version. The version that finalizes becomes
  the current version of the workspace.

An intermediate write does not end a run, so it does not set the final version.

### Read the current version of a workspace

The TFE-compatible API serves the current version of a workspace. The read
needs `unit:read` on the workspace.

```sh
curl -sS -H "Authorization: Bearer $TOKEN" \
  https://dustpan.example.com/api/v2/workspaces/$WORKSPACE_ID/current-state-version
```

The answer is a JSON:API document. `data.id` names the current version.

```json
{
  "data": {
    "id": "sv-0123456789abcdefghij",
    "type": "state-versions",
    "attributes": {
      "status": "finalized",
      "serial": 3,
      "lineage": "6f2c1e0a-4b8d-4f2a-9c3e-1d5b7a9c0e21",
      "md5": "3f1c9d4b7a2e5068c1d8f0a4b6e2d7c9",
      "size": 4821,
      "restored-from": ""
    },
    "links": {
      "self": "/api/v2/state-versions/sv-0123456789abcdefghij"
    }
  }
}
```

A workspace without state answers 404 with the code `workspace.no-state`.

### Read the state of a stack

The Pulumi Cloud-compatible API serves the state of a stack. The read needs
`unit:read` on the stack.

```sh
curl -sS -H "Authorization: token $TOKEN" \
  https://dustpan.example.com/api/stacks/acme/example-project/dev/export
```

The answer holds the deployment, in the shape that `pulumi stack export`
prints:

```json
{
  "version": 3,
  "deployment": {
    "manifest": {},
    "resources": []
  }
}
```

A stack without state answers 200 with the version 3 and `"deployment": null`.

## The final version

The final version is the version that ends a run:

- For a stack, the end of the update sets it. The last checkpoint of the update
  is the final version.
- For a workspace, the release of the lock sets it. The last state version of
  the run is the final version.

A job that waits for the state of a run, for example a write-back job that
sends the state of a stack to Git, reads the final version. The final state
write closes that job in the same commit. So a job never reads a version from a
run that is not ended.

A write that does not end a run leaves the final version as it is.

## A duplicate final write

A final state version takes no content after it finalized. A client that sends
the same final version again gets a refusal. The server answers 403 with the
code `state-version.upload-denied`.

```json
{
  "errors": [
    {
      "status": "403",
      "title": "Forbidden",
      "detail": "Forbidden",
      "code": "state-version.upload-denied"
    }
  ]
}
```

The second write changes no version and closes no job. Push the state again
only with a new version.

## Retry after a rollback

If the server cannot write the version row, the transaction rolls back. Then
neither the state nor the version row commits, and a job that waits for the
final version keeps waiting. Push the state again: the next write starts a new
transaction.

`opendustpan doctor` does not report a state write. It checks the settings of
the install, and, with `--server`, the running server. Run it to rule out a
broken install before you push the state again.

```sh
DUSTPAN_ADMIN_TOKEN=$TOKEN opendustpan doctor --server https://dustpan.example.com
```

The server checks use the token of the client commands. Without the token, the
first check fails and the other two say that they did not run. Each line starts
with `pass`, `warn` or `fail`, and `doctor` exits 1 when a check fails. The
[doctor guide](doctor.md) lists each check and the one command that helps.
After you fix a failed check, push the state again.

## Restore an older version

A restore writes the content of an older state version as a new, current
version. It changes no infrastructure: the resources stay as they are, and the
restored state describes them again.

The restore needs `unit:write` on the unit.

```sh
curl -sS -X POST -H "Authorization: Bearer $TOKEN" \
  https://dustpan.example.com/api/v2/state-versions/$STATE_VERSION_ID/actions/restore
```

The answer holds the new version and a message.

```json
{
  "data": {
    "id": "sv-210987654321zyxwvuts",
    "type": "state-versions",
    "attributes": {
      "status": "finalized",
      "serial": 4,
      "restored-from": "sv-0123456789abcdefghij"
    }
  },
  "meta": {
    "message": "Restore writes old state as a new version. It changes no infrastructure."
  }
}
```

A version that is not finalized answers 404 with the code
`state-version.not-found`. A lock that another principal holds answers 409 with
the code `workspace.locked`.

## Masked reads of state

The routes that read a resource, diff two states, or reveal one value open the
state and mask its secret values. These routes are:

- OpenTofu: `GET /api/v2/state-versions/{state_version_id}/resources`, `GET
  /api/v2/state-versions/{state_version_id}/resources/{address}`, `GET
  /api/v2/state-versions/{state_version_id}/diff` and `POST
  /api/v2/state-versions/{state_version_id}/actions/reveal`.
- Pulumi: `GET /api/stacks/{namespace}/{project}/{stack}/state/{version}/resources`,
  the route of one resource, `GET /api/stacks/{namespace}/{project}/{stack}/state/diff`
  and `POST /api/stacks/{namespace}/{project}/{stack}/state/{version}/reveal`.

The server refuses a state for a masked read in three cases. The state is too
deep when it nests more than 100 levels. The state is too large when the work
of one masked read is more than 128 MiB. A diff counts the work of both states
together. In the third case, the state is neither too deep nor too large, but
a sensitive mark of the state names a value that the server cannot find with
one rule. This happens when a mark has more than 101 tokens, or when a mark
goes below a dynamic wrapper (an object that has only the keys `value` and
`type`) and the wrapper has two readings that both fit, or the mark needs a
second wrapper. The server does not guess, because a guess can show a secret.
The answer is 413 with the error code `state-version.too-complex` for OpenTofu
and `stack.state-too-complex` for Pulumi. For the third case, the detail of
the answer says that a sensitive mark names a value that the server cannot
find with one rule. The detail does not repeat the mark. The answer holds no
value of the state.

A refused reveal writes one audit entry of the kind `reveal` with the outcome
`failure` and no value path. The server also writes one warning to its log with
the state version. A refused diff logs both state versions, because the answer
does not tell which of the two states is too deep or too large.

To get the state itself, use the routes that do not mask: the download of a
state version for OpenTofu, and the export of a stack for Pulumi. Both need
`unit:read-secrets` when they answer secret values.

## Related

- [Check an install with doctor](doctor.md) shows each check and its command.
- [Gates and access limits on state writes](state-write-gates.md) shows what the
  server checks before it lets a state write go on, and what a refusal looks
  like.
- The [TFE-compatible API](../tfe-api.md) lists the state routes of OpenTofu.

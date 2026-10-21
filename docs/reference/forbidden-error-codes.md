# Error codes of a 403 answer

A request that the server refuses with the status 403 has one of four causes
that a program can tell apart. The answer has the stable field `errorCode` for
each cause. The `message` and the status stay as they were. A program selects
its action from `errorCode` and never from the text of `message`.

## The codes

| `errorCode` | The cause | The `message` |
| --- | --- | --- |
| `access.denied` | The caller lacks a permission for the action on the object. | `forbidden` |
| `access.ip-not-allowed` | The client address is outside the IP allowlist of the namespace. | `forbidden` |
| `session.csrf` | A request with the session cookie has no `X-Dustpan-CSRF` header, or the header is wrong. | `permission denied: the X-Dustpan-CSRF header is missing or wrong` |
| `session.other-site` | A request with the session cookie comes from another site. | `permission denied: a request with the session cookie comes from another site` |

A code never changes its meaning, and the server never removes it. The text of
`message` can change, so a program must not match on it.

The IP allowlist and a missing permission send the same `message`. Only
`errorCode` tells them apart. A person who gets `access.ip-not-allowed` must
send the request from an allowed address. A person who gets `access.denied`
needs a grant.

When a request has more than one cause, the code follows this order: `session.csrf`,
`session.other-site`, `access.ip-not-allowed`, `access.denied`. The `message`
follows the same order.

## The body

The tool-neutral API and the Pulumi Cloud-compatible API send:

```json
{"code": 403, "message": "forbidden", "errorCode": "access.ip-not-allowed"}
```

`code` is the HTTP status. It is not the machine code.

## Where the codes appear

The codes appear in the answers of:

- the installation API under `/api/server`
- the tool-neutral APIs under `/api/dustpan/v1`, `/api/inventory`, `/api/audit`
  and `/api/runners` (the management routes)
- the Pulumi Cloud-compatible API under `/api`

The APIs of the notification destinations, the profiles and the registry sent
the code `auth.forbidden` for all four causes. They now send the codes of the
table.

Some 403 answers have no code from this table because their cause is another
one. Examples are a refused start of a job (the codes `job.policy-denied`,
`job.agent-denied` and `job.gate-enforced`), a preferences request of a caller that is
not a person (`preferences.person-required`), and an answer to a
runner (`retired`). Those answers keep their own code or have none.

The answers of the TFE-compatible API and of the OpenTofu HTTP backend keep
their shape and have no code from this table.

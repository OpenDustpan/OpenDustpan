# Set the proxy rules of a job

This guide shows how to save the proxy rules of a namespace and of a unit,
how to read them, and how to see the rules that a job gets.

A proxy rule says which outbound connection a job can make. A set holds the
rules of one scope. A namespace has one set, and each unit has one set. A job
gets the rules of the namespace first and the rules of the unit after them.

The paths in this guide are under `/api/dustpan/v1/`. Use the token of a
person. An empty set is valid: it has no rules and its version is 0.

## The routes

| Route | Action | Use |
| --- | --- | --- |
| `GET namespaces/{namespace}/proxy-rules` | `namespace:read` | Read the set of the namespace. |
| `PUT namespaces/{namespace}/proxy-rules` | `namespace:manage-proxy-rules` | Replace the set of the namespace. |
| `GET namespaces/{namespace}/units/{unit_id}/proxy-rules` | `unit:read` | Read the set of the unit and the joined set. |
| `PUT namespaces/{namespace}/units/{unit_id}/proxy-rules` | `unit:manage-proxy-rules` | Replace the set of the unit. |
| `GET namespaces/{namespace}/units/{unit_id}/proxy-rules/rendered` | `unit:read` | Show the joined set for one operation. For debug only. |

A save needs a fresh sign-in. The grants of a namespace administrator hold
both manage actions.

## Fields of a proxy rule

| Field | Content |
| --- | --- |
| `id` | The name of the rule, 1 to 64 bytes. |
| `operations` | A list of `plan`, `apply`, `destroy` or `refresh`. |
| `host` | The host name, at most 253 bytes. |
| `port` | The port. The default is 443. An answer shows the effective value. |
| `mode` | `inspect`, `tunnel` or `endpoint`. |
| `methods` | The HTTP methods that the rule allows. Optional. |
| `paths` | The URL paths that the rule allows, at most 64 paths of 2048 bytes. Optional. |
| `credential` | The export of a profile of the unit that the proxy adds to the request. Optional. |
| `clientCertificate` | The export of a profile of the unit that the proxy uses as client certificate. Optional. |
| `serverTrust` | The CA that the proxy trusts. The default is `system`. An answer shows the effective value. |

Mode `inspect` ends the TLS of the client, checks the method and the path of
each request, and can add a credential. Mode `tunnel` checks the host only and
passes the bytes. Mode `endpoint` serves the host on a loopback port of the
job and checks each request.

A set has at most 256 rules and at most 64 KB.

## Save a set

Send the whole set. A save replaces the stored set.

```
curl -X PUT -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -H 'If-Match: "<digest>"' \
  -d '{"rules":[{"id":"registry","operations":["plan","apply"],"host":"registry.example.com","mode":"tunnel"}]}' \
  "$SERVER/api/dustpan/v1/namespaces/acme/proxy-rules"
```

A read answers with `digest`, `version` and `updatedAt`, and with the digest
as `ETag`. Send that digest in `If-Match` to save only when nobody changed the
set after your read. A save with no `If-Match` always saves. Each save writes
an audit entry with the old and the new digest.

## Read the rules of a unit

The read of a unit answers with the set of the unit and with `joined`: the
rules of the namespace, then the rules of the unit. Each item names its scope.

## See the rendered rules

`GET .../proxy-rules/rendered?operation=plan` shows the joined rules that a
job of this operation gets. Use it to debug a rule. Do not build a tool on it:
the job gets its rules from the server, not from this route.

## Errors

| Status | Code | Cause and command |
| --- | --- | --- |
| 400 | `request.if-match-invalid` | `If-Match` is not a digest in quotes. Send the `ETag` of a read. |
| 400 | `request.invalid-path` | `unit_id` is not valid. Use the ID from the unit list. |
| 400 | `request.invalid-query` | `operation` is not `plan`, `apply`, `destroy` or `refresh`. Send one of them. |
| 404 | `unit.not-found` | No unit has this ID in the namespace. List the units of the namespace. |
| 412 | `request.precondition-failed` | The set changed after your read. Read it again and send the save again. |
| 413 | `request.body-too-large` | The body is larger than 64 KB. Remove rules. |
| 415 | `request.content-type-unsupported` | The content type is not `application/json`. Set the header. |
| 422 | `request.body-invalid` | A rule is not valid. The message names the rule and the field. Correct it. |

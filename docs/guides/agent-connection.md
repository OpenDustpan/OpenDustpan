# Document the model connection

This guide shows how to store the key of a model as a secret item, rotate
it, read the connection document, replace the egress allowlist, and
disconnect again.

A secret item holds one credential of the scope `@server`, sealed at rest.
The connection document holds references such as `itm-abc1`, never values.
No route answers a value. The route that writes the connection document is
a later change: this page covers the routes that store, read and delete.

Use the token of an administrator. Each route below needs its action on
`@server`.

## The routes

| Route | Action | Use |
| --- | --- | --- |
| `POST /api/agent/secrets` | `server:manage-agent` | Store a key. Answers 201 with the reference, the name and the time. |
| `GET /api/agent/secrets` | `server:manage-agent` | Read the items, newest first, with metadata only and no value. |
| `PUT /api/agent/secrets/{ref}` | `server:manage-agent` | Replace the value of one item, for example to rotate a key. |
| `DELETE /api/agent/secrets/{ref}` | `server:manage-agent` | Delete one item. Answers 409 while a stored document refers to it. |
| `GET /api/agent/connection` | `server:manage-agent` | Read the current document with references only and its version. Answers 404 when no document exists. |
| `DELETE /api/agent/connection` | `server:manage-agent` | Disconnect: delete the current document, each pending document and the reference rows in one transaction. |
| `GET /api/agent/formats` | `server:manage-agent` | Read the wire formats that a document can name. |
| `GET /api/server/egress-allowlist` | `server:manage-settings` | Read the egress allowlist of the installation. |
| `PUT /api/server/egress-allowlist` | `server:manage-settings` | Replace the egress allowlist. |

## Store a key

Send the name and the value. The value is at most 16 KiB.

```
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"name":"openai-key","value":"<the key>"}' \
  "$SERVER/api/agent/secrets"
```

The answer holds `ref`, `name` and `createdAt`, and no value:

```
{"ref":"itm-abc1","name":"openai-key","createdAt":"2026-11-25T12:00:00Z"}
```

Keep the reference. The document names the key by this reference only.

## Rotate a key

Send the new value to the reference. The answer holds the metadata with
the new time.

```
curl -X PUT -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"value":"<the new key>"}' \
  "$SERVER/api/agent/secrets/itm-abc1"
```

## Read the items

```
curl -H "Authorization: Bearer $TOKEN" "$SERVER/api/agent/secrets"
```

The answer holds `secrets`: the reference, the name and the time of each
item, newest first, and no value.

## Read the connection document

```
curl -H "Authorization: Bearer $TOKEN" "$SERVER/api/agent/connection"
```

The answer holds `document` and `version`. The document holds references
only. A new installation answers 404 with the code
`agent-connection.not-connected`: no default model, provider, base URL or
credential exists.

## Read the formats

```
curl -H "Authorization: Bearer $TOKEN" "$SERVER/api/agent/formats"
```

The answer is the list of the wire formats that a document can name, for
example `["chat-completions","messages"]`.

## Replace the egress allowlist

The allowlist holds host names and CIDR rules for the model calls. A rule
never opens an always-denied range such as loopback, link-local or a cloud
metadata address, whatever the list says. Send the whole list: a change
replaces the stored list.

```
curl -H "Authorization: Bearer $TOKEN" "$SERVER/api/server/egress-allowlist"
```

A new installation answers `{"hosts":[],"cidrs":[]}`.

```
curl -X PUT -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"hosts":["models.example.com"],"cidrs":[{"cidr":"10.0.0.0/8","port":443,"http":false}]}' \
  "$SERVER/api/server/egress-allowlist"
```

The answer holds the stored list.

## Disconnect and delete

Disconnect first. It deletes the current document, each pending document
and the reference rows in one transaction.

```
curl -X DELETE -H "Authorization: Bearer $TOKEN" "$SERVER/api/agent/connection"
```

Then delete the item.

```
curl -X DELETE -H "Authorization: Bearer $TOKEN" "$SERVER/api/agent/secrets/itm-abc1"
```

A delete while a stored document refers to the item answers 409 with the
code `agent-connection.secret-in-use`, and the item stays. Disconnect with
`DELETE /api/agent/connection` first, then delete it.

## Errors

| Status | Code | Cause and command |
| --- | --- | --- |
| 400 | `agent-connection.secret-invalid` | The name, the value or the reference is not valid. Send a name of 1 to 64 bytes, a value that is not empty, and a reference of the form `itm-...` from the item list. |
| 400 | `agent-connection.allowlist-invalid` | A host or a CIDR rule is not valid, or a rule starts in an address that no range opens. Send host names and ranges such as `10.0.0.0/8`. |
| 404 | `agent-connection.secret-not-found` | No item has this reference. List the items with `GET /api/agent/secrets`. |
| 404 | `agent-connection.not-connected` | The server holds no connection document. The route that writes the document is a later change. |
| 409 | `agent-connection.secret-in-use` | A stored document refers to the item. Disconnect with `DELETE /api/agent/connection`, then delete it. |

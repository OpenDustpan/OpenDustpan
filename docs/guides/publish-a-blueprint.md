# Publish a blueprint

A platform team writes a blueprint as a draft, and publishes it as a version.
A requester uses only a published version. This guide uses the HTTP API. The
routes are in the [self-service API reference](../reference/self-service-api.md).

Grants apply at namespace level today: the token needs `blueprint:publish` on
the namespace that holds the blueprint. A grant on one blueprint comes in a
later change.

## 1. Read the draft

```sh
curl -sS -H "Authorization: Bearer $TOKEN" \
  "$SERVER/api/dustpan/v1/self-service/blueprints/$NAMESPACE/database/draft"
```

The `ETag` header holds the draft version, in quotes. Keep it for the next
step.

## 2. Write the draft

Send the whole document as the body. Send the version from step 1 in the
`If-Match` header:

```sh
curl -sS -X PUT -H "Authorization: Bearer $TOKEN" -H 'If-Match: "3"' \
  --data-binary @database.json \
  "$SERVER/api/dustpan/v1/self-service/blueprints/$NAMESPACE/database/draft"
```

A draft that changed since step 1 answers `409`. Read the draft again, then
write it again.

## 3. Publish the draft version

```sh
curl -sS -X POST -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"draftVersion": 4}' \
  "$SERVER/api/dustpan/v1/self-service/blueprints/$NAMESPACE/database/versions"
```

The server compiles every expression before it stores the version. If an
expression does not compile, the answer is `400` with the code
`self-service.blueprint-invalid`, and nothing is stored. A draft version that
is already published answers `409`: change the draft first.

## 4. Check the version

```sh
curl -sS -H "Authorization: Bearer $TOKEN" \
  "$SERVER/api/dustpan/v1/self-service/blueprints/$NAMESPACE/database/versions/1"
```

The answer shows the number, the draft version it came from, who published it,
and the Expr version that compiled it.

## Retire a version

A retired version stays in the history. A publish after a retire is allowed.

```sh
curl -sS -X POST -H "Authorization: Bearer $TOKEN" \
  "$SERVER/api/dustpan/v1/self-service/blueprints/$NAMESPACE/database/versions/1/retire"
```

# Publish a blueprint

A platform team writes a blueprint as a draft, and publishes it as a version.
A requester uses only a published version. This guide uses the HTTP API. The
routes are in the [self-service API reference](../reference/self-service-api.md).

Grants apply at namespace level today: the token needs `blueprint:publish` on
the namespace that holds the blueprint. A grant on one blueprint comes in a
later change.

## 1. Write the first draft

A new blueprint has no draft, so a read of its draft answers `404` with the
code `self-service.blueprint-not-found`. Make the first draft with a write.
The version of a blueprint that has no draft is `0`, so send `If-Match: "0"`.
Send the whole document as the body:

```sh
curl -sS -X PUT -H "Authorization: Bearer $TOKEN" -H 'If-Match: "0"' \
  -H 'Content-Type: application/json' --data-binary @database.json \
  "$SERVER/api/dustpan/v1/self-service/blueprints/$NAMESPACE/database/draft"
```

The answer is `200` with `{"version": 1}` and the `ETag` header `"1"`.

The `name` in the document must be the same as the blueprint name in the path
(`database` in this example). If the two differ, the answer is `400` with the
code `self-service.blueprint-invalid`. The same code answers any other
document that is not valid. The answer does not name the field that failed, so
check the document against the rules in the
[self-service API reference](../reference/self-service-api.md) and write it
again.

A write with no `If-Match` header answers `428`. A write with a version that is
not the current version answers `409`.

## 2. Change the draft

To change a draft that exists, read it first. Then write it with its version.

```sh
curl -sS -i -H "Authorization: Bearer $TOKEN" \
  "$SERVER/api/dustpan/v1/self-service/blueprints/$NAMESPACE/database/draft"
```

The `ETag` header holds the draft version, in quotes. Send that version in the
`If-Match` header of the write:

```sh
curl -sS -X PUT -H "Authorization: Bearer $TOKEN" -H 'If-Match: "1"' \
  -H 'Content-Type: application/json' --data-binary @database.json \
  "$SERVER/api/dustpan/v1/self-service/blueprints/$NAMESPACE/database/draft"
```

Each successful write adds 1 to the draft version. A draft that changed since
you read it answers `409`. Read the draft again, then write it again.

## 3. Publish the draft version

Send the draft version that you want to publish. In this example, it is `2`.

```sh
curl -sS -X POST -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"draftVersion": 2}' \
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

## Related guides

Profiles and blueprints stay the recommended path for new setups, including
for teams that already use Terragrunt. See [Why profiles and blueprints stay
the recommended path](../explanation/profiles-and-blueprints.md).

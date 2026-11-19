# Read a decision record

This guide tells you how to read one decision record that the server stored. A
decision record shows why a gate or an access limit allowed or refused an
action. See [Gates and access limits on state writes](state-write-gates.md).

## Find the record ID

When a gate refuses a state write, the refusal text names the record ID and the
read route. The ID starts with `gdr-`. An approval at a CLI cannot wait, so the
server refuses the write and names the record ID in the same way.

## Read a record of a namespace

Set `SERVER` to the base URL of the server, `TOKEN` to an access token and
`RECORD` to the record ID. Then send:

```sh
curl -sS -H "Authorization: Bearer $TOKEN" \
  "$SERVER/api/dustpan/v1/namespaces/acme/decision-records/$RECORD"
```

The answer is the stored record as JSON. It holds the record ID, the decision
point, the outcome and the gates that the server evaluated. The record of a
change decision point also holds the keys of the secret inputs of the change
(`secretKeys`). A record never holds a secret value.

The caller needs these actions on the namespace:

- `namespace:read`
- `namespace:read-gates`

## Read a record of an installation object

A record of an access limit on an installation object has its own route:

```sh
curl -sS -H "Authorization: Bearer $TOKEN" \
  "$SERVER/api/dustpan/v1/decision-records/$RECORD"
```

The caller needs the action `installation:read-gates` on the installation. The
installation operator and site administrator roles hold it.

No decision point of an installation object writes a record in this version.
So each read of this route answers 404 until such a decision point ships.

## Errors

- 404 with the code `gate.not-found`: the server has no record with this ID that
  you can read. The answer is the same for an ID that is not valid, a record of
  another namespace, a record that does not exist, a caller with no credential
  and a caller without the read action. The body never tells you that a record
  exists. The server writes an audit entry for each read that the access check
  refuses. It writes no entry for an ID that is not valid or a record that does
  not exist.
- 503 with the code `gate.unavailable`: the decision record store cannot answer.
  Check the health of the server with `opendustpan health --listen <address>`, then send the
  request again.

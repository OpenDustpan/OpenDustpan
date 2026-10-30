# Audit export API

`GET /api/audit/export` sends the audit entries of one scope as a stream of
JSON lines. The server reads the entries one at a time, so the size of the
log does not change the memory that an export uses.

## Request

| Query value | Required | Meaning |
| --- | --- | --- |
| `organization` | yes | The namespace to export, or `@server` for the installation log. |
| `format` | no | `json`. This is the default and the one format that the server sends. |
| `fromSequence` | no | The first sequence to send. A positive number. The default is 1. |
| `from` | no | Send only entries with a time at or after this value, in Unix milliseconds. |
| `to` | no | Send only entries with a time at or before this value, in Unix milliseconds. |

A value that is not valid answers 400 with the code `request.invalid-query`
and writes no entry. The server never changes a value to make it valid.

The route needs the action `namespace:read-audit` on the namespace, or
`server:read-audit` on `@server`.

The server reads from `fromSequence` to the last sequence that the log holds
when the export starts. An entry that a person appends later is not in the
export. A time range reads each entry from `fromSequence` and keeps the
entries inside the range, so use `fromSequence` to skip the start of a large
log.

## Response

The answer is 200 with the content type `application/x-ndjson`. Each line is
one JSON object that ends with a line feed. Lines go oldest first.

A line has the fields of an entry of `GET /api/audit/entries`, and the field
`namespace`, which holds the scope of the entry. The hash covers the scope, so
a reader needs it to compute the hash again. The fields `sequence`,
`prevHash` and `hash` give the place of the entry in the chain. `prevHash` is
empty for the first entry of a scope.

If the server cannot read an entry after it sent the first line, it ends the
transfer with an error. A client must treat a transfer that did not end
cleanly as an export that is not whole.

## The entry of the export

Before the first line, the server writes one entry with the kind `audit-read`
in the scope that it reads. The action is the read action of the route, and
the object is `audit-export <scope>` followed by the values of the request.
The entry is not in the export that wrote it. If the server cannot write it,
the answer is 500 with the code `audit.export-not-recorded` and no line.

## Check a range with no help from the server

1. Read the lines in order.
2. Check that each `sequence` is one more than the sequence before it.
3. Check that each `prevHash` is equal to the `hash` of the line before it.
4. Compute the hash of each line again, as the guide to the audit log
   describes, from the fields of the line and its `prevHash`. Check that it is
   equal to `hash`.

The first line of a range from the middle of the log has the `hash` of the
entry before the range as its `prevHash`. The check cannot prove that this
entry is the one the log holds. To prove it, compare the first and last
`hash` of the range with a signed checkpoint or with a copy that you hold.

An entry from a time when the chain was off has an empty `hash`. The check
cannot prove such an entry unchanged.

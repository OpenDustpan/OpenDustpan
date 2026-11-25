# Job-start idempotency

An agent can start a job again after a failure without a second job. Send the
header `Idempotency-Key` with the start request. A retry with the same key and
the same request returns the answer of the first start.

The route is `POST /api/dustpan/v1/namespaces/{namespace}/jobs`. The
[jobs API](jobs-api.md) describes the body of the start and the permissions.

## Idempotency-Key retention

The server keeps the record of a job start for 24 hours after the start.
The record is in the durable store of the installation.

| Time of the retry | Result |
| --- | --- |
| 24 hours or less after the first start | The server returns the stored answer. The status is 201, the answer has the header `Idempotent-Replayed: true`, and the job ID is the same. The server starts no second job. |
| More than 24 hours after the first start | The server treats the key as new. It starts a new job with a new job ID. |

A restart or a failover of the server does not remove the record. A retry
within the 24 hours finds it.

The key belongs to the caller. The same key from another caller starts another
job.

A start with the same key and another request within the 24 hours fails with
422 and the code `request.idempotency-key-reused`. To read the jobs of the
namespace, send `GET /api/dustpan/v1/namespaces/{namespace}/jobs`. Add
`?unit={unit_id}` to read only the jobs of the workspace. Each job has
`createdAt`. Find the job whose `createdAt` is closest to, and after, the time
of the first request. The answer does not show the key. To start the other request, send a new
`Idempotency-Key`.

The retention is 24 hours. This version has no setting for it.

## Related

- [Jobs API](jobs-api.md)
